package bobbin_test

import (
  "context"
  "errors"
  bobbin "github.com/smcty/bobbin"
  "reflect"
  "testing"
  "time"
)

func nothing() error { return nil }

func TestNewTomb(t *testing.T) {
  tb := &bobbin.Bobbin{}
  checkState(t, tb, false, false, bobbin.ErrStillAlive)
}

func TestGo(t *testing.T) {
  tb := &bobbin.Bobbin{}
  alive := make(chan bool)
  tb.Go(func() error {
    alive <- true
    tb.Go(func() error {
      alive <- true
      <-tb.Dying()
      return nil
    })
    <-tb.Dying()
    return nil
  })
  <-alive
  <-alive
  checkState(t, tb, false, false, bobbin.ErrStillAlive)
  tb.Kill(nil)
  tb.Wait()
  checkState(t, tb, true, true, nil)
}

func TestGoErr(t *testing.T) {
  first := errors.New("first error")
  second := errors.New("first error")
  tb := &bobbin.Bobbin{}
  alive := make(chan bool)
  tb.Go(func() error {
    alive <- true
    tb.Go(func() error {
      alive <- true
      return first
    })
    <-tb.Dying()
    return second
  })
  <-alive
  <-alive
  tb.Wait()
  checkState(t, tb, true, true, first)
}

func TestGoPanic(t *testing.T) {
  // ErrDying being used properly, after a clean death.
  tb := &bobbin.Bobbin{}
  tb.Go(nothing)
  tb.Wait()
  defer func() {
    err := recover()
    if err != "bobbin.Go called after all goroutines terminated" {
      t.Fatalf("Wrong panic on post-death bobbin.Go call: %v", err)
    }
    checkState(t, tb, true, true, nil)
  }()
  tb.Go(nothing)
}

func TestGoAfterAllPreviousReturns(t *testing.T) {
  tb := &bobbin.Bobbin{}
  tb.Go(nothing)
  time.Sleep(time.Millisecond * 100)
  tb.Go(nothing)
  tb.Wait()

  // Invoke Wait a second time to test idempotency.
  tb.Wait()
}

func TestKill(t *testing.T) {
  // a nil reason flags the goroutine as dying
  tb := &bobbin.Bobbin{}
  tb.Kill(nil)
  checkState(t, tb, true, false, nil)

  // a non-nil reason now will override Kill
  err := errors.New("some error")
  tb.Kill(err)
  checkState(t, tb, true, false, err)

  // another non-nil reason won't replace the first one
  tb.Kill(errors.New("ignore me"))
  checkState(t, tb, true, false, err)

  tb.Go(nothing)
  tb.Wait()
  checkState(t, tb, true, true, err)
}

func TestKillf(t *testing.T) {
  tb := &bobbin.Bobbin{}

  err := tb.Killf("BO%s", "OM")
  if s := err.Error(); s != "BOOM" {
    t.Fatalf(`Killf("BO", "OM"): want "BOOM", got %q`, s)
  }
  checkState(t, tb, true, false, err)

  // another non-nil reason won't replace the first one
  tb.Killf("ignore me")
  checkState(t, tb, true, false, err)

  tb.Go(nothing)
  tb.Wait()
  checkState(t, tb, true, true, err)
}

func TestErrDying(t *testing.T) {
  // ErrDying being used properly, after a clean death.
  tb := &bobbin.Bobbin{}
  tb.Kill(nil)
  tb.Kill(bobbin.ErrDying)
  checkState(t, tb, true, false, nil)

  // ErrDying being used properly, after an errorful death.
  err := errors.New("some error")
  tb.Kill(err)
  tb.Kill(bobbin.ErrDying)
  checkState(t, tb, true, false, err)

  // ErrDying being used badly, with an alive bobbin.
  tb = &bobbin.Bobbin{}
  defer func() {
    err := recover()
    if err != "bobbin: Kill with ErrDying while still alive" {
      t.Fatalf("Wrong panic on Kill(ErrDying): %v", err)
    }
    checkState(t, tb, false, false, bobbin.ErrStillAlive)
  }()
  tb.Kill(bobbin.ErrDying)
}

func TestKillErrStillAlivePanic(t *testing.T) {
  tb := &bobbin.Bobbin{}
  defer func() {
    err := recover()
    if err != "bobbin: Kill with ErrStillAlive" {
      t.Fatalf("Wrong panic on Kill(ErrStillAlive): %v", err)
    }
    checkState(t, tb, false, false, bobbin.ErrStillAlive)
  }()
  tb.Kill(bobbin.ErrStillAlive)
}

func checkState(t *testing.T, tb *bobbin.Bobbin, wantDying, wantDead bool, wantErr error) {
  select {
  case <-tb.Dying():
    if !wantDying {
      t.Error("<-Dying: should block")
    }
  default:
    if wantDying {
      t.Error("<-Dying: should not block")
    }
  }
  seemsDead := false
  select {
  case <-tb.Dead():
    if !wantDead {
      t.Error("<-Dead: should block")
    }
    seemsDead = true
  default:
    if wantDead {
      t.Error("<-Dead: should not block")
    }
  }
  if err := tb.Err(); err != wantErr {
    t.Errorf("Err: want %#v, got %#v", wantErr, err)
  }
  if wantDead && seemsDead {
    waitErr := tb.Wait()
    switch {
    case waitErr == bobbin.ErrStillAlive:
      t.Errorf("Wait should not return ErrStillAlive")
    case !reflect.DeepEqual(waitErr, wantErr):
      t.Errorf("Wait: want %#v, got %#v", wantErr, waitErr)
    }
  }
}

type BobbinCtxPair struct {
  bob *bobbin.Bobbin

  // Parent context of the above bobbin. If the context is cancelled,
  // the bobbin must get killed.
  ctx context.Context
}

// Returns bobbins [self, child, grandchild].
func constructHierarchicalBobbins() []BobbinCtxPair {
  var list []BobbinCtxPair
  ctx := context.Background()

  // bob will be kill if ctx is cancelled.
  // childCtx will be cancelled when bob is killed.
  bob, childCtx := bobbin.WithContext(ctx)
  list = append(list, BobbinCtxPair{bob, ctx})

  // childbob will be killed if childCtx is cancelled.
  // grandChildCtx will be cancelled when childbob is killed.
  childbob, grandChildCtx := bobbin.WithContext(childCtx)
  list = append(list, BobbinCtxPair{childbob, childCtx})

  grandChildBob, _ := bobbin.WithContext(grandChildCtx)
  list = append(list, BobbinCtxPair{grandChildBob, grandChildCtx})

  return list
}

func TestParentBobbinKillsChildren(t *testing.T) {
  bobbinFamily := constructHierarchicalBobbins()

  bobbinFamily[0].bob.Kill(nil)
  bobbinFamily[2].bob.Wait()
  <-bobbinFamily[2].ctx.Done()

  bobbinFamily[1].bob.Wait()
  bobbinFamily[1].ctx.Done()
}

func TestKillingChildDoesNotKillParent(t *testing.T) {
  bobbinFamily := constructHierarchicalBobbins()

  bobbinFamily[1].bob.Kill(nil)
  time.Sleep(time.Millisecond * 50)
  if !bobbinFamily[0].bob.Alive() {
    t.Error("Parent Bobbin is expected to be alive.")
  }
}
