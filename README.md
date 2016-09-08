Simple jobs scheduler, which runs predefined number of processors, accepts new jobs, put them in a queue, reschedule if needed, 
send logs and results to user functions. When shutdown gracefully complete all running jobs, log and results messages and exit.

```go
// Example of using jobs scheduler. Read simple jobs from file if it was saved, schedule jobs. Then read string from
// stdin and schedule new job. On each start remove the log file. When user input `exit` phrase gracefully shutdown
// scheduler and exit.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/satori/go.uuid"
	s "github.com/viktor-br/jobs-scheduler"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"
)

// JobTest test job, which should implements required method GetID() string
type JobTest struct {
	ID string
}

// JobResultTest test job result, which should implements required methods
// IsDone() and IsCorrupted(). The last returns true if processing failed 
// and no need to restart the job.
type JobResultTest struct {
}

// getID implement Job interface
func (job JobTest) GetID() string {
	return job.ID
}

// IsDone implement JobResult interface
func (jobResult JobResultTest) IsDone() bool {
	return true
}

// IsCorrupted implement JobResult interface
func (jobResult JobResultTest) IsCorrupted() bool {
	return false
}

func main() {
	loggerFilename := "test.log"
	jobsFilename := "unprocessed.json"
	var buf bytes.Buffer
	// Init logger with output to file
	f, err := os.OpenFile(loggerFilename, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		fmt.Printf("Failed to open file: %v", err)
		return
	}
	err = f.Truncate(0)
	if err != nil {
		fmt.Printf("Failed to clear log file: %v", err)
		return
	}
	defer f.Close()

	logger := log.New(&buf, "", log.Ldate|log.Ltime|log.LUTC)
	logger.SetOutput(f)

	// Create scheduler with simple processor, which sleeps 3 seconds to emulate it's doing something.
	scheduler := s.NewJobsScheduler(func(s.Job) s.JobResult {
		time.Sleep(3 * time.Second)
		return JobResultTest{}
	})
	// Set up options
	scheduler.Option(s.MaxTries(3), s.ProcessorsNum(2))
	scheduler.AddLogger(func(msg string) {
		logger.Println(msg)
	})
	// Add function which process results flow
	scheduler.AddResultOutput(func(res s.JobResult) {
		logger.Println("got result job")
	})
	scheduler.Run()

	// Read previously saved uncompleted jobs from file
	savedJobs := map[string]JobTest{}
	raw, err := ioutil.ReadFile(jobsFilename)
	if err != nil {
		fmt.Println("Cannot read uncompleted jobs file")
	}
	json.Unmarshal(raw, &savedJobs)

	// Schedule uncompleted jobs
	for _, v := range savedJobs {
		err = scheduler.Add(v)
		if err != nil {
			fmt.Println(err.Error())
		}
	}

	// Read input from stdin while exit not specified
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("cmd> ")
		cmd, _ := reader.ReadString('\n')
		cmd = strings.TrimSpace(cmd)

		if cmd == "exit" {
			scheduler.Shutdown()
			break
		} else if cmd != "" {
			job := JobTest{ID: uuid.NewV4().String()}
			scheduler.Add(job)
		}
	}
	// When use input exit, wait scheduler will be completed
	scheduler.Wait()

	// Read from scheduler jobs, which were not processed and save to file
	j := scheduler.GetUncompletedJobs()
	b, err := json.Marshal(j)
	if err == nil {
		ioutil.WriteFile(jobsFilename, b, 0644)
	}
}
```