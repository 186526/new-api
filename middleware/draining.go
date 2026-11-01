package middleware

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// Draining reports whether the process has received a shutdown signal and
// stopped accepting new requests.
type Draining struct {
	flag atomic.Bool
}

func NewDraining() *Draining {
	return &Draining{}
}

// Begin marks the process as draining. From then on the readiness probe
// answers 503 and new relay requests are refused with 503, while requests
// already in flight keep running.
func (d *Draining) Begin() {
	d.flag.Store(true)
}

func (d *Draining) IsDraining() bool {
	return d.flag.Load()
}

// Readiness answers the readiness probe. The endpoint leaves the Service
// endpoints as soon as the process starts draining.
func (d *Draining) Readiness(c *gin.Context) {
	if d.IsDraining() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "draining"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// RejectNewRequests refuses relay traffic once the process is draining, so a
// request that reaches the pod before the Service stops routing to it fails
// fast instead of starting a stream that the shutdown timeout would cut.
func (d *Draining) RejectNewRequests() gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.IsDraining() {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{
					"message": "server is shutting down, retry on another instance",
					"type":    "server_draining",
				},
			})
			return
		}
		c.Next()
	}
}
