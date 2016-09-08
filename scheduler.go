package scheduler

import (
	"fmt"
	"github.com/satori/go.uuid"
	"sync"
)

// Job describes interface, which used by job processor. Scheduler requires only getID() method, which should provide
// unique identifier.
type Job interface {
	getID() string
}

// JobResult result of processor.
// - IsDone() should return true if job successfully done.
// - IsCorrupted() should return true if job failed and no need to restart.
type JobResult interface {
	IsDone() bool
	IsCorrupted() bool
}

// Task represents internal job, which wrap scheduled job.
type Task struct {
	ID    string
	tries int
	job   Job
}

// TaskResult wrap job result.
type TaskResult struct {
	taskID    string
	jobResult JobResult
}

// JobsScheduler accepts new job, schedules to available processor, collect results.
type JobsScheduler struct {
	config            *config
	processorsChannel chan Task
	processorsDone    chan struct{}
	loggerChannel     chan string
	loggerDone        chan struct{}
	jobsChannel       chan Task
	jobsDone          chan struct{}
	resultsChannel    chan TaskResult
	wg                *sync.WaitGroup
	jobsSent          map[string]Task
	jobsScheduled     map[string]Task
	loggers           []func(string)
	resultOutputs     []func(JobResult)
	processor         func(Job) JobResult
}

// NewJobsScheduler creates new jobs scheduler. You need to provide only processor.
func NewJobsScheduler(processor func(Job) JobResult) *JobsScheduler {
	scheduler := &JobsScheduler{
		&config{},
		make(chan Task, 3),
		make(chan struct{}),
		make(chan string, 20),
		make(chan struct{}),
		make(chan Task, 20),
		make(chan struct{}),
		make(chan TaskResult, 20),
		new(sync.WaitGroup),
		make(map[string]Task),
		make(map[string]Task),
		[]func(string){},
		[]func(JobResult){},
		processor,
	}
	// Default values
	scheduler.Option(MaxTries(3), ProcessorsNum(2))
	return scheduler
}

// Option set up scheduler options.
func (scheduler *JobsScheduler) Option(opts ...Option) {
	for _, opt := range opts {
		opt(scheduler)
	}
}

// Shutdown gracefully stops scheduler.
func (scheduler *JobsScheduler) Shutdown() {
	close(scheduler.processorsDone)
}

// AddLogger appends new logger to loggers list.
func (scheduler *JobsScheduler) AddLogger(logger func(string)) {
	scheduler.loggers = append(scheduler.loggers, logger)
}

// AddResultOutput appends function, which invokes when new result appeared.
func (scheduler *JobsScheduler) AddResultOutput(output func(JobResult)) {
	scheduler.resultOutputs = append(scheduler.resultOutputs, output)
}

// Add append new job to scheduler to process.
func (scheduler *JobsScheduler) Add(job Job) (error) {
	if job == nil {
		return fmt.Errorf("Empty job not allowed")
	}
	jobUnit := Task{ID: uuid.NewV4().String(), tries: 0, job: job}
	select {
	case scheduler.jobsChannel <- jobUnit:
		scheduler.loggerChannel <- fmt.Sprintf("%s scheduled successfully", jobUnit.job.getID())
	default:
		scheduler.loggerChannel <- fmt.Sprintf("%s could not be scheduled", jobUnit.job.getID())
		return fmt.Errorf("Internal error. Unable to schedule job: %s", jobUnit.job.getID())
	}

	return nil
}

// GetUncompletedJobs returns list of jobs, which are in queue.
func (scheduler *JobsScheduler) GetUncompletedJobs() map[string]Job {
	res := map[string]Job{}
	for k, v := range scheduler.jobsScheduled {
		res[k] = v.job
	}
	return res
}

// Run starts scheduler.
func (scheduler *JobsScheduler) Run() {
	go processLogMsg(scheduler)

	// jobs goroutine is responsible for accumulating all jobs (interaction with remote server) and schedule
	// its processing, in case of exit or remote server unavailability -- save jobs to a filesystem for later
	// execute.
	go scheduleJobs(scheduler)

	// Logger goroutine
	for i := int64(0); i < scheduler.config.processorsNum; i++ {
		go processJob(scheduler, i)
	}
}
// Wait properly shutdown of scheduler.
func (scheduler *JobsScheduler) Wait() {
	scheduler.wg.Wait()
	// Send signal to close jobs goroutine
	scheduler.jobsDone <- struct{}{}
	<-scheduler.jobsDone
	close(scheduler.jobsDone)
	// Send signal to close logger goroutine
	scheduler.loggerDone <- struct{}{}
	<-scheduler.loggerDone
	close(scheduler.loggerDone)
}

// processLogMsg controls log file, receives msg from goroutine. When signal of done received, read all messages from
// logs channel and exit.
func processLogMsg(scheduler *JobsScheduler) {
	sendMsgFunc := func(str string) {
		if scheduler.loggers != nil {
			for _, fn := range scheduler.loggers {
				go fn(str) // fire and forget
			}
		}
	}
	for {
		select {
		case str := <-scheduler.loggerChannel:
			sendMsgFunc(str)

		case <-scheduler.loggerDone:
			scheduler.loggerChannel <- fmt.Sprintf("log processor: exit signal received")
			close(scheduler.loggerChannel)
			for msg := range scheduler.loggerChannel {
				sendMsgFunc(msg)
			}
			scheduler.loggerDone <- struct{}{}
			return
		}
	}
}

// scheduleJobs put jobs to channel for processors. Read results channel
// and exclude done job from its map of sent jobs. Read incoming jobs and if free processors available put next job to
// the channel. When signal of done received, wait for all results and exit.
func scheduleJobs(scheduler *JobsScheduler) {
	counter := int64(0)
	var jobUnit Task

	scheduleNextJobs := func() {
		for k, job := range scheduler.jobsScheduled {
			scheduler.processorsChannel <- job
			counter++
			delete(scheduler.jobsScheduled, k)
			scheduler.jobsSent[k] = job
			if counter >= scheduler.config.processorsNum {
				break
			}
		}
	}
	// Read saved jobs
	for {
		select {
		case <-scheduler.jobsDone:
			scheduler.loggerChannel <- fmt.Sprintf("scheduler: exit signal received")
		// Save jobs to the file
			for k, v := range scheduler.jobsSent {
				scheduler.jobsScheduled[k] = v
			}

			scheduler.jobsDone <- struct{}{}
			return
		case job := <-scheduler.jobsChannel:
		// Try to put job to processor channel (ch)
			scheduler.jobsScheduled[job.ID] = job
			scheduler.loggerChannel <- fmt.Sprintf("scheduler: %s added to the queue", job.ID)
			if counter < scheduler.config.processorsNum {
				scheduleNextJobs()
			}
		case jobResultUnit := <-scheduler.resultsChannel:
			scheduler.loggerChannel <- fmt.Sprintf("scheduler: %s result received", jobResultUnit.taskID)
			counter--
			jobUnit = scheduler.jobsSent[jobResultUnit.taskID]
			jobUnit.tries++
			delete(scheduler.jobsSent, jobResultUnit.taskID)
			if (jobResultUnit.jobResult != nil && jobResultUnit.jobResult.IsDone() && !jobResultUnit.jobResult.IsCorrupted()) || jobUnit.tries >= scheduler.config.maxTries {
				if scheduler.resultOutputs != nil {
					for _, fn := range scheduler.resultOutputs {
						go fn(jobResultUnit.jobResult) // fire and forget
					}
				}
			} else {
				scheduler.jobsScheduled[jobResultUnit.taskID] = jobUnit
				scheduler.loggerChannel <- fmt.Sprintf("scheduler: %s rescheduled", jobResultUnit.taskID)
			}
			if counter < scheduler.config.processorsNum {
				scheduleNextJobs()
			}
		}
	}
}

// processJob reads job to process, if signal of done received, exit.
func processJob(scheduler *JobsScheduler, index int64) {
	scheduler.wg.Add(1)
	defer func() {
		scheduler.wg.Done()
		scheduler.loggerChannel <- fmt.Sprintf("processor #%d: exit", index)
	}()

	for {
		select {
		case <-scheduler.processorsDone:
		// Done
			scheduler.loggerChannel <- fmt.Sprintf("processor #%d: exit signal received", index)
			return
		case jobUnit := <-scheduler.processorsChannel:
		// Got new job
			scheduler.loggerChannel <- fmt.Sprintf("processor #%d: %s started", index, jobUnit.ID)
			res := scheduler.processor(jobUnit.job)
			jobUnit.tries++
			scheduler.loggerChannel <- fmt.Sprintf("processor #%d: %s completed", index, jobUnit.ID)

			jobResultUnit := TaskResult{jobUnit.ID, res}
				select {
				case scheduler.resultsChannel <- jobResultUnit:
					scheduler.loggerChannel <- fmt.Sprintf("processor #%d: %s result pushed successfully", index, jobUnit.ID)
				default:
					scheduler.loggerChannel <- fmt.Sprintf("processor #%d: %s result push failed", index, jobUnit.ID)
				}
		}
	}
}
