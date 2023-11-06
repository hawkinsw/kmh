package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/exp/constraints"
)

type Number interface {
	constraints.Integer | constraints.Float
}

var (
	size           = flag.Uint64("size", 512, "Enter the amount of data periodically sent from the server.")
	buffer         = flag.Int("buffer", 512, "Enter the local buffer size.")
	url            = flag.String("URL", "localhost:443/periodic", "The URL for a Periodic endpoint.")
	insecure       = flag.Bool("insecure", true, "Allow the server to have self-signed certificates.")
	timeoutSeconds = flag.Uint("timeout", 5, "How long the test will last (in seconds).")
)

func average[T Number](values []T) float64 {
	total := float64(0)
	for _, v := range values {
		total += float64(v)
	}
	return total / float64(len(values))
}

type KmhCalculator struct {
	context context.Context
	waiter  *sync.WaitGroup
	size    uint64
	current uint64
	start   time.Time
	last    time.Time
	filter  time.Duration
	deltas  []int64
	body    io.ReadCloser
	debug   bool
}

func NewKmhCalculator(context context.Context, waiter *sync.WaitGroup, size uint64, body io.ReadCloser) KmhCalculator {
	return KmhCalculator{
		context: context, waiter: waiter, size: size, start: time.Now(),
		last: time.Now(), body: body, debug: false, filter: 1 * time.Second,
	}
}

func (sr *KmhCalculator) Deltas() []int64 {
	return sr.deltas
}

func (sr *KmhCalculator) Read(p []byte) (n int, err error) {
	n, err = sr.body.Read(p)

	if sr.debug {
		fmt.Printf("Starting with current: %v\n", sr.current)
		fmt.Printf("n: %v\n", n)
	}
	packetized := uint64(n)
	for sr.current+packetized >= sr.size {
		if sr.debug {
			fmt.Printf("current + countDown: %v\n", sr.current+packetized)
		}
		packetized -= (sr.size - sr.current)
		sr.current = 0
		now := time.Now()
		recentDelta := now.Sub(sr.last)
		sr.last = now

		if recentDelta > sr.filter {
			if sr.debug {
				fmt.Printf("Adding a delta: %v\n", recentDelta)
			}
			sr.deltas = append(sr.deltas, recentDelta.Nanoseconds())
		} else {
			if sr.debug {
				fmt.Printf("Skipping a delta: %v\n", recentDelta)
			}
		}

		if sr.debug {
			fmt.Printf("Had a full packet!\n")
			fmt.Printf("Countdown remaining: %v\n", packetized)
		}
	}
	sr.current += packetized
	if sr.debug {
		fmt.Printf("Ending with current: %v\n", sr.current)
	}

	if sr.context.Err() != nil {
		fmt.Printf("Ending a statistical read\n")
		sr.waiter.Done()
		err = io.EOF
	}
	return
}

func PrintOptions(size uint64, buffer int, url string, insecure bool, timeout time.Duration) {
	fmt.Printf("Size of data periodically sent from server: %v\n", size)
	fmt.Printf("Local buffer size                         : %v\n", buffer)
	fmt.Printf("Server URL                                : %v\n", url)
	fmt.Printf("Allow self-signed certificates?           : %v\n", insecure)
	fmt.Printf("Test timeout                              : %v\n", timeout)
}

func main() {
	flag.Parse()

	client := http.DefaultClient
	transport := &http.Transport{}
	transport.ReadBufferSize = *buffer
	transport.TLSClientConfig = &tls.Config{}
	transport.TLSClientConfig.InsecureSkipVerify = *insecure
	client.Transport = transport

	timeoutDuration := time.Duration(*timeoutSeconds) * time.Second

	PrintOptions(*size, *buffer, *url, *insecure, timeoutDuration)

	response, err := client.Get(fmt.Sprintf("https://%v?size=%v", *url, *size))
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	context, contextCanceler := context.WithTimeout(context.Background(), timeoutDuration)
	defer contextCanceler()

	waiter := sync.WaitGroup{}

	waiter.Add(1)
	kmhCalculator := NewKmhCalculator(context, &waiter, *size, response.Body)

	go func() { _, err = io.ReadAll(&kmhCalculator) }()

	if err != nil {
		fmt.Printf("error: %v.\n", err)
		return
	}
	waiter.Wait()

	average := average(kmhCalculator.Deltas()) / float64(time.Second.Nanoseconds())

	impliedBufferSize := average * float64((*size))

	fmt.Printf("KMH Implied Buffer Size: %.2f Kb\n", impliedBufferSize)
}
