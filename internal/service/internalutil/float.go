package internalutil

// Float64To32 converts a float64 slice to float32.
func Float64To32(src []float64) []float32 {
	if len(src) == 0 {
		return nil
	}
	dst := make([]float32, len(src))
	for i, v := range src {
		dst[i] = float32(v)
	}
	return dst
}
