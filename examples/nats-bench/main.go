// Copyright 2015-2018 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"sync"
	"time"

	"github.com/nats-io/go-nats"
	"github.com/nats-io/go-nats/bench"
)

// Some sane defaults
const (
	DefaultNumMsgs         = 100000
	DefaultNumPubs         = 1
	DefaultNumPubsRoutines = 100
	DefaultNumSubs         = 0
	DefaultMessageSize     = 128
)

func usage() {
	log.Fatalf("Usage: nats-bench [-s server (%s)] [--tls] [-np NUM_PUBLISHERS] [-ns NUM_SUBSCRIBERS] [-n NUM_MSGS] [-ms MESSAGE_SIZE] [-csv csvfile] <subject>\n", nats.DefaultURL)
}

var benchmark *bench.Benchmark

func main() {
	var urls = flag.String("s", nats.DefaultURL, "The nats server URLs (separated by comma)")
	var tls = flag.Bool("tls", false, "Use TLS Secure Connection")
	var numPubs = flag.Int("np", DefaultNumPubs, "Number of Concurrent Publishers")
	var numPubsRoutines = flag.Int("npr", DefaultNumPubsRoutines, "Number of routines for one publisher")
	var numSubs = flag.Int("ns", DefaultNumSubs, "Number of Concurrent Subscribers")
	var numMsgs = flag.Int("n", DefaultNumMsgs, "Number of Messages to Publish")
	var msgSize = flag.Int("ms", DefaultMessageSize, "Size of the message.")
	var csvFile = flag.String("csv", "", "Save bench data to csv file")
	var userCreds = flag.String("creds", "", "User Credentials File")

	log.SetFlags(0)
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) != 1 {
		usage()
	}

	if *numMsgs <= 0 {
		log.Fatal("Number of messages should be greater than zero.")
	}

	// Connect Options.
	opts := []nats.Option{nats.Name("NATS Benchmark")}

	// Use UserCredentials
	if *userCreds != "" {
		opts = append(opts, nats.UserCredentials(*userCreds))
	}

	// Use TLS specfied
	if *tls {
		opts = append(opts, nats.Secure(nil))
	}

	benchmark = bench.NewBenchmark("NATS", *numSubs, *numPubs**numPubsRoutines)

	var startwg sync.WaitGroup
	var donewg sync.WaitGroup

	donewg.Add(*numPubs**numPubsRoutines + *numSubs)

	// Run Subscribers first
	startwg.Add(*numSubs)
	for i := 0; i < *numSubs; i++ {
		nc, err := nats.Connect(*urls, opts...)
		if err != nil {
			log.Fatalf("Can't connect: %v\n", err)
		}
		defer nc.Close()

		go runSubscriber(nc, &startwg, &donewg, *numMsgs**numPubsRoutines, *msgSize)
	}
	startwg.Wait()

	// Now Publishers
	startwg.Add(*numPubs * *numPubsRoutines)
	pubCounts := bench.MsgsPerClient(*numMsgs / *numPubsRoutines, *numPubs)
	for i := 0; i < *numPubs; i++ {
		nc, err := nats.Connect(*urls, opts...)
		if err != nil {
			log.Fatalf("Can't connect: %v\n", err)
		}
		defer nc.Close()

		for j := 0; j < *numPubsRoutines; j++ {
			go runPublisher(nc, &startwg, &donewg, pubCounts[j], *msgSize)
		}
	}

	log.Printf("Starting benchmark [msgs=%d, msgsize=%d, pubs=%d, subs=%d]\n", *numMsgs, *msgSize, *numPubs, *numSubs)

	startwg.Wait()
	donewg.Wait()

	benchmark.Close()

	fmt.Print(benchmark.Report())

	if len(*csvFile) > 0 {
		csv := benchmark.CSV()
		ioutil.WriteFile(*csvFile, []byte(csv), 0644)
		fmt.Printf("Saved metric data in csv file %s\n", *csvFile)
	}
}

func runPublisher(nc *nats.Conn, startwg, donewg *sync.WaitGroup, numMsgs int, msgSize int) {
	startwg.Done()

	args := flag.Args()
	subj := args[0]
	//var payload []byte
	var msg []byte
	if msgSize > 0 {
		//payload = make([]byte, msgSize)
		msg = make([]byte, msgSize)
	}

	start := time.Now()

	for i := 0; i < numMsgs; i++ {
		nc.Publish(subj, msg)
		//_, err := nc.Request(subj, []byte(payload), time.Second)
		//if err != nil {
		//	if nc.LastError() != nil {
		//		log.Fatalf("%v for request", nc.LastError())
		//	}
		//	log.Fatalf("%v for request", err)
		//}
	}
	nc.Flush()
	benchmark.AddPubSample(bench.NewSample(numMsgs, msgSize, start, time.Now(), nc))

	donewg.Done()
}

func runSubscriber(nc *nats.Conn, startwg, donewg *sync.WaitGroup, numMsgs int, msgSize int) {
	args := flag.Args()
	subj := args[0]

	received := 0
	ch := make(chan time.Time, 2)
	sub, _ := nc.Subscribe(subj, func(msg *nats.Msg) {
		received++
		//nc.Publish(msg.Reply, []byte("reply"))
		if received == 1 {
			ch <- time.Now()
		}
		if received >= numMsgs {
			ch <- time.Now()
		}
	})
	sub.SetPendingLimits(-1, -1)
	nc.Flush()
	startwg.Done()

	start := <-ch
	end := <-ch
	benchmark.AddSubSample(bench.NewSample(numMsgs, msgSize, start, end, nc))
	nc.Close()
	donewg.Done()
}
