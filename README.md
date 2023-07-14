Installation and usage
----------------------

Bobbin is a fork of the tomb package with one minor but useful change.
It allows invoking the Go() method even when all the previously tracked
goroutines return.

This allows users to use Bobbin as a true WaitGroup replacment for
tracking and waiting for all the child goroutines.

Example Usage:

```go
func Test() { 
  ctx := context.Background()
  // bob will be killed if ctx is cancelled.
  // childCtx will be cancelled if bob is killed.
  bob, childCtx := bobbin.WithContext(ctx) 
  // Wait for all the child goroutines to finish.
  defer bob.Wait()

  // Start a child go-routine.
  bob.Go(func() { 
      childBob, grandChildCtx := bobbin.WithContext(ctx)
      // Before exiting child goroutine, wait for all the grandchild
      // goroutines to finish.
      defer childBob.Wait()

      // Child goroutine starts a grand child. 
      childBob.Go(func(ctx context.Context) { 
         // Do something.  
      })
  })
}
```


See [gopkg.in/tomb.v2](https://gopkg.in/tomb.v2) for documentation and usage details.
