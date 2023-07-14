// Copyright (c) 2011 - Gustavo Niemeyer <gustavo@niemeyer.net>
//
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//
//     * Redistributions of source code must retain the above copyright notice,
//       this list of conditions and the following disclaimer.
//     * Redistributions in binary form must reproduce the above copyright notice,
//       this list of conditions and the following disclaimer in the documentation
//       and/or other materials provided with the distribution.
//     * Neither the name of the copyright holder nor the names of its
//       contributors may be used to endorse or promote products derived from
//       this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR
// CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
// EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
// PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
// PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
// LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
// NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

// The bobbin package handles clean goroutine tracking and termination.
//
// The zero value of a Bobbin is ready to handle the creation of a tracked
// goroutine via its Go method, and then any tracked goroutine may call
// the Go method again to create additional tracked goroutines at
// any point.
//
// If any of the tracked goroutines returns a non-nil error, or the
// Kill or Killf method is called by any goroutine in the system (tracked
// or not), the bobbin Err is set, Alive is set to false, and the Dying
// channel is closed to flag that all tracked goroutines are supposed
// to willingly terminate as soon as possible.
//
// Once all tracked goroutines terminate, the Dead channel is closed,
// and Wait unblocks and returns the first non-nil error presented
// to the bobbin via a result or an explicit Kill or Killf method call,
// or nil if there were no errors.
//
// It is okay to create further goroutines via the Go method while
// the bobbin is in a dying state. The final dead state is only reached
// once all tracked goroutines terminate, at which point calling
// the Go method again will cause a runtime panic.
//
// Tracked functions and methods that are still running while the bobbin
// is in dying state may choose to return ErrDying as their error value.
// This preserves the well established non-nil error convention, but is
// understood by the bobbin as a clean termination. The Err and Wait
// methods will still return nil if all observed errors were either
// nil or ErrDying.
//
// For background and a detailed example, see the following blog post:
//
//   http://blog.labix.org/2011/10/09/death-of-goroutines-under-control
//
package bobbin

import (
  "errors"
  "fmt"
  "sync"
)

// A Bobbin tracks the lifecycle of one or more goroutines as alive,
// dying or dead, and the reason for their death.
//
// See the package documentation for details.
type Bobbin struct {
  m      sync.Mutex
  alive  int
  dying  chan struct{}
  dead   chan struct{}
  reason error

  // context.Context is available in Go 1.7+.
  parent interface{}
  child  map[interface{}]childContext
}

type childContext struct {
  context interface{}
  cancel  func()
  done    <-chan struct{}
}

var (
  ErrStillAlive = errors.New("bobbin: still alive")
  ErrDying      = errors.New("bobbin: dying")
)

func (t *Bobbin) init() {
  t.m.Lock()
  if t.dead == nil {
    t.dead = make(chan struct{})
    t.dying = make(chan struct{})
    t.reason = ErrStillAlive
  }
  t.m.Unlock()
}

// Dead returns the channel that can be used to wait until
// all goroutines have finished running.
func (t *Bobbin) Dead() <-chan struct{} {
  t.init()
  return t.dead
}

// Dying returns the channel that can be used to wait until
// t.Kill is called.
func (t *Bobbin) Dying() <-chan struct{} {
  t.init()
  return t.dying
}

// Wait blocks until all goroutines have finished running, and
// then returns the reason for their death.
func (t *Bobbin) Wait() error {
  t.init()
  <-t.dead
  t.m.Lock()
  reason := t.reason
  t.m.Unlock()
  return reason
}

// Go runs f in a new goroutine and tracks its termination.
//
// If f returns a non-nil error, t.Kill is called with that
// error as the death reason parameter.
//
// It is f's responsibility to monitor the bobbin and return
// appropriately once it is in a dying state.
//
// It is safe for the f function to call the Go method again
// to create additional tracked goroutines. Once all tracked
// goroutines return, the Dead channel is closed and the
// Wait method unblocks and returns the death reason.
//
// Calling the Go method after all tracked goroutines return
// causes a runtime panic. For that reason, calling the Go
// method a second time out of a tracked goroutine is unsafe.
func (t *Bobbin) Go(f func() error) {
  t.init()
  t.m.Lock()
  defer t.m.Unlock()
  select {
  case <-t.dead:
    panic("bobbin.Go called after all goroutines terminated")
  default:
  }
  t.alive++
  go t.run(f)
}

func (t *Bobbin) run(f func() error) {
  err := f()
  t.m.Lock()
  defer t.m.Unlock()
  t.alive--
  if t.alive == 0 || err != nil {
    t.kill(err)
    if t.alive == 0 {
      close(t.dead)
    }
  }
}

// Kill puts the bobbin in a dying state for the given reason,
// closes the Dying channel, and sets Alive to false.
//
// Althoguh Kill may be called multiple times, only the first
// non-nil error is recorded as the death reason.
//
// If reason is ErrDying, the previous reason isn't replaced
// even if nil. It's a runtime error to call Kill with ErrDying
// if t is not in a dying state.
func (t *Bobbin) Kill(reason error) {
  t.init()
  t.m.Lock()
  defer t.m.Unlock()
  t.kill(reason)
}

func (t *Bobbin) kill(reason error) {
  if reason == ErrStillAlive {
    panic("bobbin: Kill with ErrStillAlive")
  }
  if reason == ErrDying {
    if t.reason == ErrStillAlive {
      panic("bobbin: Kill with ErrDying while still alive")
    }
    return
  }
  if t.reason == ErrStillAlive {
    t.reason = reason
    close(t.dying)
    for _, child := range t.child {
      child.cancel()
    }
    t.child = nil
    return
  }
  if t.reason == nil {
    t.reason = reason
    return
  }
}

// Killf calls the Kill method with an error built providing the received
// parameters to fmt.Errorf. The generated error is also returned.
func (t *Bobbin) Killf(f string, a ...interface{}) error {
  err := fmt.Errorf(f, a...)
  t.Kill(err)
  return err
}

// Err returns the death reason, or ErrStillAlive if the bobbin
// is not in a dying or dead state.
func (t *Bobbin) Err() (reason error) {
  t.init()
  t.m.Lock()
  reason = t.reason
  t.m.Unlock()
  return
}

// Alive returns true if the bobbin is not in a dying or dead state.
func (t *Bobbin) Alive() bool {
  return t.Err() == ErrStillAlive
}
