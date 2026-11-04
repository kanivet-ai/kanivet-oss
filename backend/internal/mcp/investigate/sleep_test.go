package investigate

import "time"

func sleepMs(n int) { time.Sleep(time.Duration(n) * time.Millisecond) }
