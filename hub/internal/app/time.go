package app

import "time"

func nowMS() int64 {
	return time.Now().UnixMilli()
}
