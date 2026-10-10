package httpx

import "errors"

func asXRPC(err error, target **XRPCError) bool {
	return errors.As(err, target)
}
