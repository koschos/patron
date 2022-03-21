package main

import (
	"context"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/aws/aws-sdk-go/service/sqs/sqsiface"
	"github.com/beatlabs/patron"
	patrongrpc "github.com/beatlabs/patron/client/grpc"
	patronsqs "github.com/beatlabs/patron/component/sqs"
	"github.com/beatlabs/patron/encoding/json"
	"github.com/beatlabs/patron/examples"
	"github.com/beatlabs/patron/log"
	"google.golang.org/grpc"
)

const (
	awsRegion      = "eu-west-1"
	awsID          = "test"
	awsSecret      = "test"
	awsToken       = "token"
	awsSQSEndpoint = "http://localhost:4566"
	awsSQSQueue    = "patron"
)

func init() {
	err := os.Setenv("PATRON_LOG_LEVEL", "debug")
	if err != nil {
		log.Fatalf("failed to set log level env var: %v", err)
	}
	err = os.Setenv("PATRON_JAEGER_SAMPLER_PARAM", "1.0")
	if err != nil {
		log.Fatalf("failed to set sampler env vars: %v", err)
	}
	err = os.Setenv("PATRON_HTTP_DEFAULT_PORT", "50004")
	if err != nil {
		log.Fatalf("failed to set default patron port env vars: %v", err)
	}
}

func main() {
	name := "sqs"
	version := "1.0.0"
	ctx := context.Background()

	service, err := patron.New(name, version)
	if err != nil {
		log.Fatalf("failed to set up service: %v", err)
	}

	cc, err := patrongrpc.Dial("localhost:50006", grpc.WithInsecure())
	if err != nil {
		log.Fatalf("failed to dial grpc connection: %v", err)
	}
	defer func() {
		_ = cc.Close()
	}()

	greeterClient := examples.NewGreeterClient(cc)

	// Initialise SQS
	sqsAPI := sqs.New(getAWSSession(awsSQSEndpoint))
	sqsCmp, err := createSQSComponent(sqsAPI, greeterClient)
	if err != nil {
		log.Fatalf("failed to create sqs component: %v", err)
	}

	err = service.WithComponents(sqsCmp.cmp).Run(ctx)
	if err != nil {
		log.Fatalf("failed to create and run service: %v", err)
	}
}

type sqsComponent struct {
	cmp     patron.Component
	greeter examples.GreeterClient
}

func createSQSComponent(api sqsiface.SQSAPI, greeter examples.GreeterClient) (*sqsComponent, error) {
	sqsCmp := sqsComponent{
		greeter: greeter,
	}

	cmp, err := patronsqs.New("sqs-cmp", awsSQSQueue, api, sqsCmp.Process, patronsqs.PollWaitSeconds(5))
	if err != nil {
		return nil, err
	}
	sqsCmp.cmp = cmp

	return &sqsCmp, nil
}

func (ac *sqsComponent) Process(_ context.Context, btc patronsqs.Batch) {
	for _, msg := range btc.Messages() {
		logger := log.FromContext(msg.Context())
		var u examples.User

		err := json.DecodeRaw(msg.Body(), &u)
		if err != nil {
			logger.Errorf("failed to decode message: %v", err)
			msg.NACK()
			continue
		}

		logger.Infof("request processed: %v, sending request to the gRPC service", u.String())
		reply, err := ac.greeter.SayHello(msg.Context(), &examples.HelloRequest{Firstname: u.GetFirstname(), Lastname: u.GetLastname()})
		if err != nil {
			logger.Errorf("failed to send request: %v", err)
			msg.NACK()
		}

		logger.Infof("reply from the gRPC service: %s", reply.GetMessage())
		// We can either acknowledge the whole batch or each message individually.
		err = msg.ACK()
		if err != nil {
			logger.Errorf("failed to acknowledge message with id %s: %v", msg.ID(), err)
		}
	}

	// The commented code below can be used to acknowledge batch of messages instead of each single message
	// logger := log.FromContext(ctx)
	//
	// // We can either acknowledge the whole batch or each message individually.
	// failed, err := btc.ACK()
	// if err != nil {
	// 	return err
	// }
	//
	// for _, msg := range failed {
	// 	logger.Warnf("failed to acknowledge message with id: %s", msg.ID())
	// }
}

func getAWSSession(endpoint string) *session.Session {
	// 15 attempts 1 seconds separated to retrieve valid session
	var s *session.Session = nil
	var err error = nil
	for i := 0; i < 15; i++ {
		s, err = session.NewSession(
			&aws.Config{
				Region:      aws.String(awsRegion),
				Credentials: credentials.NewStaticCredentials(awsID, awsSecret, awsToken),
			},
			&aws.Config{Endpoint: aws.String(endpoint)},
		)
		if err == nil {
			return s
		}
		time.Sleep(1 * time.Second)
	}
	// this will panic if error is not null
	return session.Must(s, err)
}
