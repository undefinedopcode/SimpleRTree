package SimpleRTree

type VectorBBox [4]float64

const (
	VECTOR_BBOX_MIN_X = 0
	VECTOR_BBOX_MIN_Y = 1
	VECTOR_BBOX_MAX_X = 2
	VECTOR_BBOX_MAX_Y = 3
)

func newVectorBBox (MinX, MinY, MaxX, MaxY float64) (VectorBBox){
	return [4]float64{MinX, MinY, MaxX, MaxY}
}

func bbox2VectorBBox (b BBox) (VectorBBox){
	return newVectorBBox(b.MinX, b.MinY, b.MaxX, b.MaxY)
}

/**
 Code from
 https://github.com/slimsag/rand/blob/master/simd/vec64.go
*/
// Implemented in vectorBBox.s
func vectorBBoxExtend(b1, b2 VectorBBox) VectorBBox

func (b1 VectorBBox) toBBox () BBox {
	return BBox{
		MinX: b1[VECTOR_BBOX_MIN_X],
		MinY: b1[VECTOR_BBOX_MIN_Y],
		MaxX: b1[VECTOR_BBOX_MAX_X],
		MaxY: b1[VECTOR_BBOX_MAX_Y],
	}
}
