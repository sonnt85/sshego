package sshego

import (
	"io"
	"os"

	ssh "github.com/glycerine/sshego/xendor/github.com/glycerine/xcryptossh"
)

// Shovel shovels data from an io.ReadCloser to an io.WriteCloser
// in an independent go routine started by Shovel::Start().
// You can request that the shovel stop by closing ReqStop,
// and wait until Done is closed to know that it is finished.
type shovel struct {
	Halt *ssh.Halter

	// logging functionality, off by default
	DoLog     bool
	LogReads  io.Writer
	LogWrites io.Writer
}

// make a new Shovel
func newShovel(doLog bool) *shovel {
	return &shovel{
		Halt:      ssh.NewHalter(),
		DoLog:     doLog,
		LogReads:  os.Stdout,
		LogWrites: os.Stdout,
	}
}

type readerNilCloser struct{ io.Reader }

func (rc *readerNilCloser) Close() error { return nil }

type writerNilCloser struct{ io.Writer }

func (wc *writerNilCloser) Close() error { return nil }

// Start starts the shovel doing an io.Copy from r to w. The
// goroutine that is running the copy will close the Ready
// channel just before starting the io.Copy. The
// label parameter allows reporting on when a specific shovel
// was shut down.
func (s *shovel) Start(w io.WriteCloser, r io.ReadCloser, label string) {

	if s.DoLog {
		// TeeReader returns a Reader that writes to w what it reads from r.
		// All reads from r performed through it are matched with
		// corresponding writes to w. There is no internal buffering -
		// the write must complete before the read completes.
		// Any error encountered while writing is reported as a read error.
		r = &readerNilCloser{io.TeeReader(r, s.LogReads)}
		w = &writerNilCloser{io.MultiWriter(w, s.LogWrites)}
	}

	go func() {
		var err error
		var n int64
		defer func() {
			s.Halt.MarkDone()
			p("shovel %s copied %d bytes before shutting down", label, n)
		}()
		s.Halt.MarkReady()
		n, err = io.Copy(w, r)
		if err != nil {
			// don't freak out, the network connection got closed most likely.
			// e.g. read tcp 127.0.0.1:33631: use of closed network connection
			//panic(fmt.Sprintf("in Shovel '%s', io.Copy failed: %v\n", label, err))
			return
		}
	}()
	go func() {
		<-s.Halt.ReqStopChan()
		r.Close() // causes io.Copy to finish
		w.Close()
		s.Halt.MarkDone()
	}()
}

// stop the shovel goroutine. returns only once the goroutine is done.
func (s *shovel) Stop() {
	s.Halt.RequestStop()
	<-s.Halt.DoneChan()
}

// a shovelPair manages the forwarding of a bidirectional
// channel, such as that in forwarding an ssh connection.
type shovelPair struct {
	AB   *shovel
	BA   *shovel
	Halt *ssh.Halter

	DoLog bool
}

// make a new shovelPair
func newShovelPair(doLog bool) *shovelPair {
	pair := &shovelPair{
		AB:   newShovel(doLog),
		BA:   newShovel(doLog),
		Halt: ssh.NewHalter(),
	}
	pair.Halt.AddDownstream(pair.AB.Halt)
	pair.Halt.AddDownstream(pair.BA.Halt)
	return pair
}

// Start the pair of shovels. abLabel will label the a<-b shovel. baLabel will
// label the b<-a shovel.
func (s *shovelPair) Start(a io.ReadWriteCloser, b io.ReadWriteCloser, abLabel string, baLabel string) {
	s.AB.Start(a, b, abLabel)
	<-s.AB.Halt.ReadyChan()
	s.BA.Start(b, a, baLabel)
	<-s.BA.Halt.ReadyChan()
	s.Halt.MarkReady()

	// if one stops, shut down the other
	go func() {
		select {
		case <-s.Halt.ReqStopChan():
		case <-s.Halt.DoneChan():
		case <-s.AB.Halt.ReqStopChan():
		case <-s.AB.Halt.DoneChan():
		case <-s.BA.Halt.ReqStopChan():
		case <-s.BA.Halt.DoneChan():
		}
		s.AB.Stop()
		s.BA.Stop()
		s.Halt.RequestStop()
		s.Halt.MarkDone()
	}()
}

func (s *shovelPair) Stop() {
	s.Halt.RequestStop()
	s.AB.Stop()
	s.BA.Stop()
}
