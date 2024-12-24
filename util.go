package gatewayfile

func pick[T any](m map[string][]T, key string) (t T) {
	if len(m) == 0 {
		return t
	}
	values := m[key]
	if len(values) == 0 {
		return t
	}
	return values[0]
}
