package blobserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"code.uber.internal/infra/kraken/lib/dockerregistry/image"
	"code.uber.internal/infra/kraken/origin/client"

	"code.uber.internal/go-common.git/x/log"
)

// BlobRepairer initializes Repair context with number of concurrent workers,
// number of retries on error, default connection timeout and target host
// that should handle all blob transfers
type BlobRepairer struct {
	context     context.Context       // request context
	hostname    string                // target nodes that handles blob transfers
	blobAPI     client.BlobTransferer // blob file pusher/puller
	numWorkers  int                   // number of cocurrent blob transer workers
	numRetries  int                   // number of retries in a case of an error
	retryDelay  time.Duration         // number of milliseconds to delay between the retries << retries
	connTimeout time.Duration         // default read/write timeout on HTTP connection

	sync.Mutex
}

// DigestRepairMessage represents the result of repair operation of a digest
// on a host hostname
type DigestRepairMessage struct {
	Hostname string `json:"host"`
	Digest   string `json:"digest"`
	Result   string `json:"rs"`
}

type count32 int32

func (c *count32) increment() int32 {
	return atomic.AddInt32((*int32)(c), 1)
}

// BatchRepair repairs a batch of digest items concurrently
// it writes result for every repaired item to http response writer
func (br *BlobRepairer) BatchRepair(digests []*image.Digest, writer http.ResponseWriter) {
	var (
		wg  sync.WaitGroup
		pos count32 = -1
	)

	for i := 0; i < br.numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-br.context.Done():
					//abandon the ship when the request is cancelled on a client side,
					return
				default:
					idx := pos.increment()
					if int(idx) >= len(digests) {
						return
					}

					d := digests[idx]
					rs := "OK"
					if err := br.repairDigest(d); err != nil {
						log.WithFields(log.Fields{
							"digest": d,
						}).Errorf("failed to repair digest item: %s", err)
						rs = fmt.Sprintf("error: %s", err)
					}
					lm := &DigestRepairMessage{
						Hostname: br.hostname,
						Digest:   d.Hex(),
						Result:   rs,
					}
					// need to sync writing to a response
					br.Lock()

					if err := json.NewEncoder(writer).Encode(lm); err != nil {
						log.WithFields(log.Fields{
							"digest": d,
						}).Error("failed to encode digest repair message: %s", err)
					}
					writer.(http.Flusher).Flush()
					br.Unlock()
				}
			}
		}()
	}

	wg.Wait()
}

func (br *BlobRepairer) repairDigest(digest *image.Digest) error {
	var err error
	for i := 0; i < br.numRetries; i++ {
		if err = br.blobAPI.PushBlob(*digest); err != nil {
			time.Sleep(br.retryDelay * (2 << uint(br.numRetries)))
			continue
		}
		return err
	}
	return nil
}