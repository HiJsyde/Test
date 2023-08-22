package main

import (
	"log"
	"net"
	"net/rpc"
	"sync"
	"sync/atomic"
)

// Cell is used as the return type for the testing framework.
type Cell struct {
	X, Y int
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
	AliveCells []Cell
	World      [][]byte
}

// Store some values during the TCP connection
type ThreadArg struct {
	turn       *int
	aliveCells *int
	pauseFlag  *bool
	exitFlag   *bool
	mutex      *sync.Mutex
	worldPtr   *[][]byte
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

var globalLock sync.Mutex
var globalMap = make(map[int]*ThreadArg)

// Find the number of the alive cells in the neighbors
func getAliveCellsNumber(world [][]byte, x int, y int, width int, height int) int {
	numAlices := 0
	for cx := -1; cx <= 1; cx++ {
		for cy := -1; cy <= 1; cy++ {
			mx, my := x+cx, y+cy
			if mx == x && my == y {
				continue
			}
			if mx < 0 {
				mx = width - 1
			}
			if mx >= width {
				mx = 0
			}
			if my < 0 {
				my = height - 1
			}
			if my >= height {
				my = 0
			}
			if world[my][mx] == 255 {
				numAlices++
			}
		}
	}
	return numAlices
}

// Find alive cells
func findAliveCells(world [][]byte, width int, height int) []Cell {
	aliveCells := make([]Cell, 0)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if world[y][x] == 255 {
				cell := Cell{x, y}
				aliveCells = append(aliveCells, cell)
			}
		}
	}
	return aliveCells
}

// A worker which operates one part of the image
func worker(p GofParam, channel chan int,
	x1 int, y1 int, x2 int, y2 int, worldPtr **[][]byte, tmpWorldPtr **[][]byte,
	numAliveCells *atomic.Int32, pauseFlag *bool, exitFlag *bool, mutex *sync.Mutex) {
	for {
		// Wait for the notification of the master
		cmd := <-channel
		if cmd == 0 {
			// Terminate the thread
			break
		}
		// Perform the task
		for y := y1; y < y2; y++ {
			for x := x1; x < x2; x++ {
				if *pauseFlag {
					// Pause the thread if necessary
					(*mutex).Lock()
					(*mutex).Unlock()
				}
				if *exitFlag {
					// Terminate the thread
					channel <- 1
					return
				}
				numAlive := getAliveCellsNumber(**worldPtr, x, y, p.ImageWidth, p.ImageHeight)
				if (**worldPtr)[y][x] == 255 {
					if numAlive >= 2 && numAlive <= 3 {
						(**tmpWorldPtr)[y][x] = 255
						(*numAliveCells).Add(1)
					} else {
						(**tmpWorldPtr)[y][x] = 0
					}
				} else {
					if numAlive == 3 {
						(**tmpWorldPtr)[y][x] = 255
						(*numAliveCells).Add(1)
					} else {
						(**tmpWorldPtr)[y][x] = 0
					}
				}
			}
		}
		channel <- 1
	}
}

// GOL engine
type GolEngine struct{}

// Start the GOF engine
func (engine *GolEngine) StartGofEngine(p GofParam, reply *GofReply) error {
	// Add a record for this connection in order to store some parameters
	globalLock.Lock()
	threadArg := new(ThreadArg)
	globalMap[p.Id] = threadArg

	(*threadArg).turn = new(int)
	*((*threadArg).turn) = 0

	(*threadArg).aliveCells = new(int)
	*((*threadArg).aliveCells) = 0

	(*threadArg).exitFlag = new(bool)
	*((*threadArg).exitFlag) = false

	(*threadArg).pauseFlag = new(bool)
	*((*threadArg).pauseFlag) = false

	(*threadArg).mutex = new(sync.Mutex)

	(*threadArg).worldPtr = new([][]byte)

	globalLock.Unlock()

	// Create a 2D slice to store the world.
	world := p.World
	tmpWorld := make([][]byte, p.ImageHeight)
	for i := range tmpWorld {
		tmpWorld[i] = make([]byte, p.ImageWidth)
	}

	// compute the size of block
	numRemainder := 0
	blockRow := 0
	blockCol := 0
	mCol := p.Threads / p.ImageHeight
	numRowInc, numColInc1, numColInc2 := 0, 0, 0
	if mCol == 0 || (p.Threads == p.ImageHeight) {
		blockRow = p.ImageHeight / p.Threads
		blockCol = p.ImageWidth
		numRowInc = p.ImageHeight % p.Threads
	} else {
		blockRow = 1
		blockCol = p.ImageWidth / mCol
		numRemainder = p.Threads % p.ImageHeight
		numColInc1 = p.ImageWidth % mCol
		numColInc2 = p.ImageWidth % (mCol + 1)
	}

	(*threadArg).worldPtr = &world
	tmpWorldPtr := &tmpWorld
	var numAliveCells atomic.Int32

	// Create one channel for each worker
	workerChannels := make([]chan int, p.Threads)
	for i := 0; i < p.Threads; i++ {
		workerChannels[i] = make(chan int)
	}

	// Task different worker threads to operate on different parts of the image in parallel
	iTask := 0
	for y, iy := 0, 0; y < p.ImageHeight; iy++ {
		dy := blockRow
		if iy < numRowInc {
			dy++
		}
		for x, ix := 0, 0; x < p.ImageWidth; ix++ {
			// compute the bounding of the block
			dx := blockCol
			if iy < numRemainder {
				dx = p.ImageWidth / (mCol + 1)
				if ix < numColInc2 {
					dx++
				}
			} else {
				if ix < numColInc1 {
					dx++
				}
			}
			// Start a goroutine to compute one part of the image
			x1, y1, x2, y2 := x, y, x+dx, y+dy
			go worker(p, workerChannels[iTask], x1, y1, x2, y2, &(*threadArg).worldPtr, &tmpWorldPtr,
				&numAliveCells, (*threadArg).pauseFlag, (*threadArg).exitFlag, (*threadArg).mutex)
			iTask++
			x += dx
		}
		y += dy
	}

	for *((*threadArg).turn) < p.Turns {
		numAliveCells.Store(0)

		if *((*threadArg).pauseFlag) {
			// Pause the thread if necessary
			(*((*threadArg).mutex)).Lock()
			(*((*threadArg).mutex)).Unlock()
		}

		// Notify the workers to compute the new image
		for i := 0; i < p.Threads; i++ {
			workerChannels[i] <- 1
		}

		// wait for all workers
		for i := 0; i < p.Threads; i++ {
			<-workerChannels[i]
		}

		if *((*threadArg).exitFlag) {
			// Terminate the thread
			break
		}

		*((*threadArg).turn)++
		*((*threadArg).aliveCells) = int(numAliveCells.Load())

		world, tmpWorld = tmpWorld, world
		(*threadArg).worldPtr, tmpWorldPtr = &world, &tmpWorld
	}

	if !*((*threadArg).exitFlag) && p.Turns > 0 {
		// Notify all workers to terminate
		for i := 0; i < p.Threads; i++ {
			workerChannels[i] <- 0
		}
	}

	// Set the reply result
	(*reply).Turns = *((*threadArg).turn)
	(*reply).World = *(*threadArg).worldPtr
	(*reply).AliveCells = findAliveCells((*reply).World, p.ImageWidth, p.ImageHeight)

	return nil
}

// Get some state values
func (engine *GolEngine) GetWorld(clientId int, reply *[][]byte) error {
	globalLock.Lock()
	threadArg := globalMap[clientId]
	globalLock.Unlock()
	*reply = *(*threadArg).worldPtr
	return nil
}

// Get the number of turns and the numberof alive cells
func (engine *GolEngine) GetIntegerValues(request RequestArg, reply *ResponseArg) error {
	globalLock.Lock()
	threadArg := globalMap[request.ClientId]
	globalLock.Unlock()
	(*reply).Turns = *threadArg.turn
	(*reply).AliveCells = *threadArg.aliveCells
	return nil
}

// Set some state values
func (engine *GolEngine) SetStateValues(request RequestArg, reply *string) error {
	globalLock.Lock()
	threadArg := globalMap[request.ClientId]
	globalLock.Unlock()

	if request.ReqType == "exit" {
		*(*threadArg).exitFlag = true
	} else if request.ReqType == "resume" {
		*(*threadArg).pauseFlag = false
		(*((*threadArg).mutex)).Unlock()
	} else if request.ReqType == "pause" {
		*(*threadArg).pauseFlag = true
		(*((*threadArg).mutex)).Lock()
	}
	return nil
}

func main() {
	// Register the GOL engine
	rpc.RegisterName("GolEngine", new(GolEngine))

	listener, err := net.Listen("tcp", ":8030")
	if err != nil {
		log.Fatal("ListenTCP error:", err)
	}
	defer listener.Close()

	for {
		// Wait for one connection
		conn, err := listener.Accept()
		if err != nil {
			log.Fatal("Accept error:", err)
		}

		// Start a thread to handle with ths connection
		go rpc.ServeConn(conn)
	}
}
