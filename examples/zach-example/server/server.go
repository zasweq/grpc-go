package main

import (
	"context"
	"flag"
	"google.golang.org/grpc"
	"io"
	"log"
	"net"
	pb "google.golang.org/grpc/examples/zach-example/zach-example"
)

type zachExampleServer struct {
	// Any state the server needs to persist
	pb.UnimplementedZachExampleServer
}

// This function runs input number through a mathematical function
func (s *zachExampleServer) MathematicalFunction(ctx context.Context, numberInput *pb.NumberInput) (*pb.NumberOutput, error) {
	numberOutput := numberInput.NumberInput
	numberOutput += 3
	return &pb.NumberOutput{NumberOutput: numberOutput}, nil
}

func (s *zachExampleServer) ZeroToNumber(numberInput *pb.NumberInput, stream pb.ZachExample_ZeroToNumberServer) error {
	for i := 0; i <= int(numberInput.NumberInput); i++ {
		if err := stream.Send(&pb.NumberOutput{NumberOutput: int32(i)}); err != nil {
			return err
		}
	}
	return nil
}

// Stream inward
func (s *zachExampleServer) SumAllNumbers(stream pb.ZachExample_SumAllNumbersServer) error {
	var sum int32
	for {
		numberInput, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&pb.NumberOutput{NumberOutput: sum})
		}
		if err != nil {
			return err
		}
		sum += numberInput.NumberInput
	}
}

// This is literally going to send numbers back and forth
func (s *zachExampleServer) NumbersBackAndForth(stream pb.ZachExample_NumbersBackAndForthServer) error {
	for {
		numberInput, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&pb.NumberOutput{NumberOutput: numberInput.NumberInput}); err != nil {
			return err
		}
	}
}

func main() {
	flag.Parse()
	lis, err := net.Listen("tcp", "localhost:10000")
	if (err != nil) {
		log.Fatalf("failed to listen: %v", err)
	}
	var opts []grpc.ServerOption // Honestly, you don't even need this, this is for tls
	grpcServer := grpc.NewServer(opts...)
	s := &zachExampleServer{}
	pb.RegisterZachExampleServer(grpcServer, s)
	grpcServer.Serve(lis)
}