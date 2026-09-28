package scdorand

type Source struct{ seed int64 }

func NewSource(seed int64) *Source         { return &Source{seed: seed} }
func NewSource_EmeryFork(seed int64) *Source { return &Source{seed: seed} }

type RandObj struct{ src *Source }

func NewRandObj(src *Source) *RandObj { return &RandObj{src: src} }

func (r *RandObj) Int63n(n int64) int64 {
	if n <= 0 {
		return 0
	}
	r.src.seed = r.src.seed*6364136223846793005 + 1442695040888963407
	v := r.src.seed % n
	if v < 0 {
		v = -v
	}
	return v
}
