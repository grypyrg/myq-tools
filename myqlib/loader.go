package myqlib

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type MySQLAdminCommand string

const (
	MYSQLADMIN        string            = "mysqladmin"
	STATUS_COMMAND    MySQLAdminCommand = "extended-status"
	VARIABLES_COMMAND MySQLAdminCommand = "variables"
	// prefix of SHOW VARIABLES keys, they are stored (if available) in the same map as the status variables
	VAR_PREFIX = "V_"
)

type Loader interface {
	getStatus() (chan MyqSample, error)
	getVars() (chan MyqSample, error)
	getInterval() time.Duration
}

// MyqSamples are K->V maps
type MyqSample map[string]interface{}

// Number of keys in the sample
func (s MyqSample) Length() int {
	return len(s)
}

// MyqState contains the current and previous SHOW STATUS outputs.  Also SHOW VARIABLES.
// Prev might be nil
type MyqState struct {
	Cur, Prev   MyqSample
	SecondsDiff float64 // Difference between Cur and Prev
	FirstUptime int64   // Uptime of our first sample this run
}

// Given a loader, get a channel of myqstates being returned
func GetState(l Loader) (chan *MyqState, error) {
	// First getVars, if possible
	varsch, varserr := l.getVars()
	// return the error if getVars fails, but not if it's just due to a missing file
	if varserr != nil && varserr.Error() != "No file given" {
		// Serious error
		return nil, varserr
	}

	// Vars fetching loop
	var latestvars MyqSample // whatever the last vars sample is will be here (may be empty)
	gotvars := make(chan bool, 1)
	
	if varserr == nil {
		// Only start up the latestvars loop if there are no errors
		go func() {
			for vars := range varsch {
				latestvars = vars
				gotvars <- true
			}
			gotvars <- true
		}()
	} else {
		gotvars <- true
	}

	// Now getStatus
	var ch = make(chan *MyqState)
	statusch, statuserr := l.getStatus()
	if statuserr != nil {
		return nil, statuserr
	}

	// Main status loop
	go func() {
		defer close(ch)

		var prev MyqSample
		var firstUptime int64
		for status := range statusch {
			// Init new state
			state := new(MyqState)
			state.Cur = status

			// Only needed for File loaders really
			if firstUptime == 0 {
				firstUptime = status["uptime"].(int64)
			}
			state.FirstUptime = firstUptime

			// Assign the prev
			if prev != nil {
				state.Prev = prev

				// Calculate timediff if there is a prev.  Only file loader?
				state.SecondsDiff = float64(status["uptime"].(int64) - prev["uptime"].(int64))

				// Skip to the next sample if SecondsDiff is < the interval
				if state.SecondsDiff < l.getInterval().Seconds() {
					continue
				}
			}
			
			// In the first loop iteration, wait for some vars to be loaded 
			if prev == nil {
				<- gotvars
			}
			// Add latest vars to status with prefix
			for k, v := range latestvars {
				newkey := fmt.Sprint(VAR_PREFIX, k)
				state.Cur[newkey] = v
			}

			// Send the state
			ch <- state

			// Set the state for the next round
			prev = status
		}
	}()

	return ch, nil
}

type loaderInterval time.Duration

func (l loaderInterval) getInterval() time.Duration {
	return time.Duration(l)
}

// Load mysql status output from a mysqladmin output file
type FileLoader struct {
	loaderInterval
	statusFile    string
	variablesFile string
}

func NewFileLoader(i time.Duration, statusFile, varFile string) *FileLoader {
	return &FileLoader{loaderInterval(i), statusFile, varFile}
}
func (l FileLoader) harvestFile(filename string) (chan MyqSample, error) {
	file, err := os.OpenFile(filename, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(file)
	var ch = make(chan MyqSample)

	// The file scanning goes into the background
	go func() {
		defer file.Close()
		defer close(ch)
		scanMySQLShowLines(scanner, ch)
	}()

	return ch, nil
}

func (l FileLoader) getStatus() (chan MyqSample, error) { 
	return l.harvestFile(l.statusFile) 
}

func (l FileLoader) getVars() (chan MyqSample, error) {
	if l.variablesFile != "" {
		return l.harvestFile(l.variablesFile)
	} else {
		return nil, errors.New("No file given")
	}
}

// SHOW output via mysqladmin on a live server
type LiveLoader struct {
	loaderInterval
	args string // other args for mysqladmin (like -u, -p, -h, etc.)
}

func NewLiveLoader(i time.Duration, args string) *LiveLoader {
	return &LiveLoader{loaderInterval(i), args}
}

// Collect output from mysqladmin and send it back in a sample
func (l LiveLoader) harvestMySQLAdmin(command MySQLAdminCommand) (chan MyqSample, error) {
	// Make sure we have MYSQLADMIN
	path, err := exec.LookPath(MYSQLADMIN)
	if err != nil {
		return nil, err
	}

	// Build the argument list
	args := []string{
		string(command), "-i",
		fmt.Sprintf("%.0f", l.getInterval().Seconds()),
	}
	if l.args != "" {
		args = append(args, l.args)
	}
	// fmt.Println( args )

	// Initialize the command
	cmd := exec.Command(path, args...)
	cleanupSubcmd(cmd)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(stdout)
	var ch = make(chan MyqSample)

	// The file scanning goes into the background
	go func() {
		defer close(ch)
		scanMySQLShowLines(scanner, ch)
	}()

	// Handle if the subcommand exits
	go func() {
		err := cmd.Wait()
		if err != nil {
			fmt.Println(MYSQLADMIN, "exited: ", err, stderr.String())
		}
	}()

	return ch, nil
}

func (l LiveLoader) getStatus() (chan MyqSample, error) { return l.harvestMySQLAdmin(STATUS_COMMAND) }

func (l LiveLoader) getVars() (chan MyqSample, error) { return l.harvestMySQLAdmin(VARIABLES_COMMAND) }
