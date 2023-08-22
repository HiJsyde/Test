package gol

import (
	"fmt"
	"sync"
	"sync/atomic"
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
func findAliveCells(world [][]byte, width int, height int) []util.Cell {
	aliveCells := make([]util.Cell, 0)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if world[y][x] == 255 {
				cell := util.Cell{x, y}
				aliveCells = append(aliveCells, cell)
			}
		}
	}
	return aliveCells
}

// A worker which operates one part of the image
func worker(p Params, c distributorChannels, channel chan int, turn *int,
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
						// The state of the cell changed
						cell := util.Cell{x, y}
						c.events <- CellFlipped{*turn, cell}
					}
				} else {
					if numAlive == 3 {
						(**tmpWorldPtr)[y][x] = 255
						(*numAliveCells).Add(1)
						// The state of the cell changed
						cell := util.Cell{x, y}
						c.events <- CellFlipped{*turn, cell}
					} else {
						(**tmpWorldPtr)[y][x] = 0
					}
				}
			}
		}
		channel <- 1
	}
}

// Timer function
func timerFunc(t *time.Ticker, events chan<- Event,
	turn *int, numAliveCells *int, pauseFlag *bool,
	exitFlag *bool, mutex *sync.Mutex) {
	for {
		select {
		case <-t.C:
			{
				if *turn < 0 || *exitFlag {
					return
				}
				if *pauseFlag {
					// Pause the thread if necessary
					(*mutex).Lock()
					(*mutex).Unlock()
				}
				events <- AliveCellsCount{*turn, (int)(*numAliveCells)}
			}
		}
	}
}

// Handle with keypresses
func handleKeyPresses(p Params, c distributorChannels, worldPtr **[][]byte,
	turn *int, pauseFlag *bool, exitFlag *bool, mutex *sync.Mutex) {
	for {
		select {
		case key := <-c.keyPresses:
			switch key {
			case 's':
				{
					if *turn < 0 {
						return
					}
					// Generate a PGM file
					c.ioCommand <- ioOutput
					c.ioFilename <- fmt.Sprintf("%dx%dx%d", p.ImageWidth, p.ImageHeight, p.Turns)
					for i := range **worldPtr {
						for j := 0; j < len((**worldPtr)[i]); j++ {
							c.ioOutput <- (**worldPtr)[i][j]
						}
					}
				}
			case 'q':
				{
					if *turn < 0 {
						return
					}

					// Set exit flag
					*exitFlag = true
				}
			case 'p':
				{
					if *turn < 0 {
						return
					}
					// pause or resume
					if *pauseFlag {
						(*mutex).Unlock()
					} else {
						(*mutex).Lock()
					}
					*pauseFlag = !*pauseFlag

					if *pauseFlag {
						fmt.Printf("The current turn is %d\n", *turn)
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
	// TODO: Create a 2D slice to store the world.
	tmpWorld := make([][]byte, p.ImageHeight)
	for i := range tmpWorld {
		tmpWorld[i] = make([]byte, p.ImageWidth)
	}

	// read the original word
	c.ioCommand <- ioInput
	c.ioFilename <- fmt.Sprintf("%dx%d", p.ImageWidth, p.ImageHeight)
	world := make([][]byte, p.ImageHeight)
	for i := range world {
		world[i] = make([]byte, p.ImageWidth)
		for j := 0; j < len(world[i]); j++ {
			world[i][j] = <-c.ioInput
			if world[i][j] == 255 {
				// Send the state if the cell is alive
				cell := util.Cell{j, i}
				c.events <- CellFlipped{0, cell}
			}
		}
	}

	turn := new(int)
	*turn = 0

	worldPtr := &world
	tmpWorldPtr := &tmpWorld
	var pauseFlag, exitFlag bool
	pauseFlag, exitFlag = false, false
	var mutex sync.Mutex

	// Start a thread to handle with keypresses
	go handleKeyPresses(p, c, &worldPtr, turn, &pauseFlag, &exitFlag, &mutex)

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

	// Start a timer goroutine
	var numAliveCells atomic.Int32

	t := time.NewTicker(2 * time.Second)
	mAliveCells := 0
	go timerFunc(t, c.events, turn, &mAliveCells, &pauseFlag, &exitFlag, &mutex)

	// TODO: Execute all turns of the Game of Life.
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
			go worker(p, c, workerChannels[iTask], turn, x1, y1, x2, y2, &worldPtr, &tmpWorldPtr,
				&numAliveCells, &pauseFlag, &exitFlag, &mutex)
			iTask++
			x += dx
		}
		y += dy
	}

	for *turn < p.Turns {
		numAliveCells.Store(0)

		if pauseFlag {
			// Pause the thread if necessary
			mutex.Lock()
			mutex.Unlock()
		}

		// Notify the workers to compute the new image
		for i := 0; i < p.Threads; i++ {
			workerChannels[i] <- 1
		}

		// wait for all workers
		for i := 0; i < p.Threads; i++ {
			<-workerChannels[i]
		}

		if exitFlag {
			// Terminate the thread
			break
		}

		*turn++

		// Report the complete of this turn
		mAliveCells = int(numAliveCells.Load())
		c.events <- TurnComplete{*turn}

		world, tmpWorld = tmpWorld, world
		worldPtr, tmpWorldPtr = &world, &tmpWorld
	}

	if !exitFlag && p.Turns > 0 {
		// Notify all workers to terminate
		for i := 0; i < p.Threads; i++ {
			workerChannels[i] <- 0
		}
	}

	if !exitFlag {
		// Find alive cells
		aliveCells := findAliveCells(world, p.ImageWidth, p.ImageHeight)

		// TODO: Report the final state using FinalTurnCompleteEvent.
		*turn = -1
		c.events <- FinalTurnComplete{*turn, aliveCells}
	}

	// Output the state of the board after all turns have completed as a PGM image
	c.ioCommand <- ioOutput
	c.ioFilename <- fmt.Sprintf("%dx%dx%d", p.ImageWidth, p.ImageHeight, p.Turns)
	for i := range world {
		for j := 0; j < len(world[i]); j++ {
			c.ioOutput <- world[i][j]
		}
	}

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{*turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
