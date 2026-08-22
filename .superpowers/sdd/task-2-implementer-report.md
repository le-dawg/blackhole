# Task 2 Fix Report

## Implemented Fixes
1. **Critical - Atomic Save Support**: Refactored the `fsnotify` watcher to reload the file based on the matching filename regardless of the specific event type. This natively supports cases where VSCode/Vim performs an atomic save sequence (e.g. `Rename` and `Remove`). 
2. **Important - Trailing Dots**: Addressed domain parsing for userlists by adding `strings.TrimSuffix(strings.ToLower(line), ".")`, which gracefully strips out the trailing dot for entries like `example.com.`.
3. **Minor - Test Flakiness**: Removed hardcoded `time.Sleep` calls in `userlist_test.go` and replaced them with an `assertEventually` helper function. This function dynamically polls for success every 10ms up to 100 times, making tests quicker and more reliable.
4. **Minor - Non-blocking FSNotify Loop**: Enclosed the file `reload()` call within a time-debounced execution triggered natively in the watcher loop. It prevents synchronously reading a file while handling events and correctly queues updates.

## Status
All fixes successfully implemented and tests pass.
