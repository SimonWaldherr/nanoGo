package interp

import "reflect"

// safeReflectSelect keeps a channel close/send race in guest code from
// escaping as a host panic. Normal channel operations use ChannelVal methods;
// select needs this small reflect-specific guard.
func safeReflectSelect(cases []reflect.SelectCase) (chosen int, recv reflect.Value, recvOK bool, err error) {
	defer func() {
		if recover() != nil {
			err = NewRuntimeError("select on closed channel")
		}
	}()
	chosen, recv, recvOK = reflect.Select(cases)
	return chosen, recv, recvOK, nil
}
