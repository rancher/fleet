package progress

import (
	"io"
	"io/ioutil"
	"sync"

	"github.com/cheggaaa/pb"
)

type Progress struct {
	pool *pb.Pool
	init bool
	once sync.Once
}

func NewProgress() *Progress {
	return &Progress{
		pool: pb.NewPool(),
	}
}

func (p *Progress) Close() error {
	if p.init {
		return p.pool.Stop()
	}
	return nil
}

func (p *Progress) TrackProgress(src string, currentSize, totalSize int64, stream io.ReadCloser) io.ReadCloser {
	p.once.Do(func() {
		if err := p.pool.Start(); err == nil {
			p.init = true
		}
	})
	progress := pb.New64(totalSize)
	progress.Set64(currentSize)
	progress.SetUnits(pb.U_BYTES)
	progress.Prefix(src)

	return ioutil.NopCloser(progress.NewProxyReader(stream))
}
