package airplay

import (
	"net/http"
	"time"
)

func httpDate() string { return time.Now().UTC().Format(http.TimeFormat) }
