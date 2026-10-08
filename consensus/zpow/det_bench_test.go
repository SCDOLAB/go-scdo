package zpow

import (
	"testing"

	"gonum.org/v1/gonum/mat"
)

func BenchmarkMatrixDet(b *testing.B) {
	const dim = 30
	data := make([]float64, dim*dim)
	for i := range data {
		data[i] = float64((i*17)%1000) + 1
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if mat.Det(mat.NewDense(dim, dim, data)) == 0 {
			b.Fatal("zero det")
		}
	}
}
