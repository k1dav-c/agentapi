package convstate

// WithSubscriptionBufSize gives the emitter's subscriptions a buffer of
// size events, the default for 0, so a test can fill one.
func WithSubscriptionBufSize(size uint) EventEmitterOption {
	return func(e *EventEmitter) {
		if size == 0 {
			e.subscriptionBufSize = defaultSubscriptionBufSize
		} else {
			e.subscriptionBufSize = size
		}
	}
}
