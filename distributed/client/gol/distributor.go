package gol

import (
	"fmt"
	"log"
	"math/rand"
	"net/rpc"
	"sync"
	"time"

	"uk.ac.bris.cs/gameoflife/util"
)

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
	keyPresses <-chan rune
}

type GofParam struct {
	Turns       int
	Threads     int
	ImageWidth  int
	ImageHeight int
	Id          int
	World       [][]byte
}

type GofReply struct {
	Turns      int
	AliveCells []util.Cell
	World      [][]byte
}

// Request argument
type RequestArg struct {
	ClientId int
	ReqType  string
}

type ResponseArg struct {
	Turns      int
	AliveCells int
	World      [][]byte
}

// Timer function
func timerFunc(t *time.Ticker, events chan<- Event,
	client *rpc.Client, pauseFlag *bool,
	exitFlag *bool, mutex *sync.Mutex, endFlag *bool, clientId int) {
	for {
		select {
		case <-t.C:
			{
				if *endFlag {
					return
				}
				// request the turn and numAliveCells from the gof engine
				reply := ResponseArg{}
				reqArg := RequestArg{clientId, "turns;numAliveCells"}
				err := client.Call("GolEngine.GetIntegerValues", reqArg, &reply)
				if err != nil {
					log.Fatal(err)
				}

				if *exitFlag {
					return
				}
				if *pauseFlag {
					// Pause the thread if necessary
					(*mutex).Lock()
					(*mutex).Unlock()
				}
				events <- AliveCellsCount{reply.Turns, reply.AliveCells}
			}
		}
	}
}

// generate the PGM file
func generate_pgm_file(client *rpc.Client, p Params, c distributorChannels,
	clientId int) {
	// Request the world state from the gof engine
	var world [][]byte
	err := client.Call("GolEngine.GetWorld", clientId, &world)
	if err != nil {
		log.Fatal(err)
	}
	// Generate a PGM file
	c.ioCommand <- ioOutput
	c.ioFilename <- fmt.Sprintf("%dx%dx%d", p.ImageWidth, p.ImageHeight, p.Turns)
	for i := range world {
		for j := 0; j < len(world[i]); j++ {
			c.ioOutput <- world[i][j]
		}
	}
}

// Handle with keypresses
func handleKeyPresses(p Params, c distributorChannels,
	client *rpc.Client, pauseFlag *bool, exitFlag *bool, mutex *sync.Mutex,
	endFlag *bool, clientId int) {
	for {
		select {
		case key := <-c.keyPresses:
			if *endFlag {
				return
			}
			switch key {
			case 's':
				{
					// generate the PGM file
					generate_pgm_file(client, p, c, clientId)
				}
			case 'q':
				{
					// generate the PGM file
					generate_pgm_file(client, p, c, clientId)

					// notify the gof engine to terminate
					var reply string
					reqArg := RequestArg{clientId, "exit"}
					client.Call("GolEngine.SetStateValues", reqArg, &reply)

					// Set exit flag
					*exitFlag = true
				}
			case 'p':
				{
					// pause or resume
					if *pauseFlag {
						// notify the gof engine to resume
						var reply string
						reqArg := RequestArg{clientId, "resume"}
						client.Call("GolEngine.SetStateValues", reqArg, &reply)
						(*mutex).Unlock()
					} else {
						// notify the gof engine to pause
						var reply string
						reqArg := RequestArg{clientId, "pause"}
						client.Call("GolEngine.SetStateValues", reqArg, &reply)
						(*mutex).Lock()
					}
					*pauseFlag = !*pauseFlag

					// request the turn from the gof engine
					reply := ResponseArg{}
					reqArg := RequestArg{clientId, "turns;numAliveCells"}
					err := client.Call("GolEngine.GetIntegerValues", reqArg, &reply)
					if err != nil {
						log.Fatal(err)
					}

					if *pauseFlag {
						fmt.Printf("The current turn is %d\n", reply.Turns)
					} else {
						fmt.Println("Continuing")
					}
				}
			}
		}
	}
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {
	// read the original world
	c.ioCommand <- ioInput
	c.ioFilename <- fmt.Sprintf("%dx%d", p.ImageWidth, p.ImageHeight)
	world := make([][]byte, p.ImageHeight)
	for i := range world {
		world[i] = make([]byte, p.ImageWidth)
		for j := 0; j < len(world[i]); j++ {
			world[i][j] = <-c.ioInput
		}
	}

	// Connect the client to Gof engine
	// host_ip := "127.0.0.1"
	host_ip := "18.234.207.121"
	client, err := rpc.Dial("tcp", host_ip+":8030")
	if err != nil {
		log.Fatal("dialing:", err)
	}

	var pauseFlag, exitFlag bool
	pauseFlag, exitFlag = false, false
	var mutex sync.Mutex
	endFlag := false

	// Create client id
	seed := rand.NewSource(time.Now().UnixNano())
	clientId := rand.New(seed).Intn(10000000)

	// Start a thread to handle with keypresses
	go handleKeyPresses(p, c, client, &pauseFlag, &exitFlag, &mutex, &endFlag, clientId)

	// Start the timer
	t := time.NewTicker(2 * time.Second)
	go timerFunc(t, c.events, client, &pauseFlag, &exitFlag, &mutex, &endFlag, clientId)

	// Start the Gof engine
	gofPram := GofParam{
		p.Turns, p.Threads, p.ImageWidth, p.ImageHeight,
		clientId, world,
	}
	var reply GofReply
	err = client.Call("GolEngine.StartGofEngine", gofPram, &reply)
	if err != nil {
		log.Fatal(err)
	}

	if !exitFlag {
		// Retriee alive cells
		aliveCells := reply.AliveCells

		// Report the final state using FinalTurnCompleteEvent.
		turn := reply.Turns
		c.events <- FinalTurnComplete{turn, aliveCells}
	}

	endFlag = true

	// Output the state of the board after all turns have completed as a PGM image
	c.ioCommand <- ioOutput
	c.ioFilename <- fmt.Sprintf("%dx%dx%d", p.ImageWidth, p.ImageHeight, p.Turns)
	for i := range reply.World {
		for j := 0; j < len(reply.World[i]); j++ {
			c.ioOutput <- reply.World[i][j]
		}
	}

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{reply.Turns, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
