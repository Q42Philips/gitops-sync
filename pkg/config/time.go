package config

import (
	"time"
)

type TimeValue time.Time

func (i *TimeValue) Set(s string) (err error) {
	var t time.Time = time.Now()
	if s != "now" && s != "" {
		// Allow a configured commit time to allow aligning GitOps commits to the original repo commit
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return err
		}
	}
	*i = TimeValue(t)
	return nil
}

func (i *TimeValue) Get() interface{} { return TimeValue(*i) }
func (i *TimeValue) String() string   { return time.Time(*i).Format(time.RFC3339) }
