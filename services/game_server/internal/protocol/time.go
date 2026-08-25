package protocol

import "time"

var now = time.Now

func unixMilliseconds() int64 {
	return now().UnixMilli()
}
