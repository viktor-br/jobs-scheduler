package scheduler

// Option scheduler config option.
type Option func(*JobsScheduler)

type config struct {
	processorsNum int64
	maxTries      int
}
// MaxTries set up max tries before job marks as failed.
func MaxTries(tries int) Option {
	return func(scheduler *JobsScheduler) {
		scheduler.config.maxTries = tries
	}
}

// ProcessorsNum set up number of processors.
func ProcessorsNum(num int) Option {
	return func(scheduler *JobsScheduler) {
		scheduler.config.processorsNum = int64(num)
	}
}
