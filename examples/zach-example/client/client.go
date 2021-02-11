package main

import (
	"context"
	"io"
	"log"
	"time"

	"google.golang.org/grpc"

	pb "google.golang.org/grpc/examples/zach-example/zach-example"
)

func runThroughMathematicalFunction(client pb.ZachExampleClient, number int32) {
	log.Printf("Running %d through Mathematical Function", number)
	ctx, cancel := context.WithTimeout(context.Background(), 10 * time.Second)
	defer cancel()

	numberInput := pb.NumberInput{NumberInput: number}

	numberOutput, err := client.MathematicalFunction(ctx, &numberInput)

	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Println(numberOutput)
}

func runThroughZeroToNumber(client pb.ZachExampleClient, number int32) {
	log.Printf("Will display 0 - %v on the screen", number)
	ctx, cancel := context.WithTimeout(context.Background(), 10 * time.Second)
	defer cancel()
	numberInput := pb.NumberInput{NumberInput: number}
	stream, err := client.ZeroToNumber(ctx, &numberInput)
	if err != nil {
		log.Fatalf("%v", err)
	}
	for {
		numberOutput, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("%v", err)
		}
		log.Printf("Number: %v", numberOutput.NumberOutput)
	}

}
// Generate a proto array of numbers from 1 - x
func generateArrayOfNumbers(length int) []*pb.NumberInput {
	var numbers []*pb.NumberInput
	for i := 0; i < length; i++ {
		numberInputToAdd := &pb.NumberInput{NumberInput: int32(i)}
		numbers = append(numbers, numberInputToAdd)
	}
	return numbers
}

func runThroughSumAllNumbers(client pb.ZachExampleClient) {
	lengthOfArrayOfNumbers := 10 // In this thing explicitly declared rather than passed in as a method
	log.Printf("Will sum from 0 - %v", lengthOfArrayOfNumbers)
	arrayOfNumbers := generateArrayOfNumbers(lengthOfArrayOfNumbers)

	ctx, cancel := context.WithTimeout(context.Background(), 10 * time.Second)
	defer cancel()

	stream, err := client.SumAllNumbers(ctx)
	if err != nil {
		log.Fatalf("%v", err)
	}

	for _, numberInput := range arrayOfNumbers {
		if err := stream.Send(numberInput); err != nil {
			log.Fatalf("%v", err)
		}
	}
	sum, err := stream.CloseAndRecv()
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("Sum (from 0 - %v): %v", lengthOfArrayOfNumbers, sum.NumberOutput)
}

func runThroughNumbersBackAndForth(client pb.ZachExampleClient) {
	lengthOfArrayOfNumbers := 10
	arrayOfNumbers := generateArrayOfNumbers(lengthOfArrayOfNumbers)

	ctx, cancel := context.WithTimeout(context.Background(), 10 * time.Second)
	defer cancel()

	stream, err := client.NumbersBackAndForth(ctx)
	if err != nil {
		log.Fatalf("%v", err)
	}

	waitc := make(chan struct{})
	go func() {
		for {
			numberOutput, err := stream.Recv()
			if err == io.EOF {
				close(waitc)
				return
			}
			if err != nil {
				log.Fatalf("%v", err)
			}
			log.Printf("Number Received: %v", numberOutput.NumberOutput)
		}
	}()
	for _, numberInput := range arrayOfNumbers {
		log.Printf("Number Sent: %v", numberInput.NumberInput)
		if err := stream.Send(numberInput); err != nil {
			log.Fatalf("%v", err)
		}
	}
	stream.CloseSend()
	<-waitc
}

func main() {
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())
	opts = append(opts, grpc.WithBlock())
	conn, err := grpc.Dial("localhost:10000", opts...)
	if err != nil {
		log.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close()
	client := pb.NewZachExampleClient(conn)

	runThroughMathematicalFunction(client, 32)

	runThroughZeroToNumber(client, 32)

	runThroughSumAllNumbers(client)

	runThroughNumbersBackAndForth(client)

	// Number to number
}