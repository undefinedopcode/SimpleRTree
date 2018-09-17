package SimpleRTree

import (
	"log"
	"math"
	"container/heap"
	"text/template"
	"bytes"
	"fmt"
	"strings"
)

type Interface interface {
	GetPointAt(i int) (x1, y1 float64)        // Retrieve point at position i
	Len() int                                 // Number of elements
	Swap(i, j int)                            // Swap elements with indexes i and j
}

const MAX_POSSIBLE_ENTRIES = 9

type Options struct {
	MAX_ENTRIES int
}

type SimpleRTree struct {
	options  Options
	nodes []Node
	points FlatPoints
	built bool
	// Store pool of pools so that between algorithms it uses a channel (thread safe) within one algorithm it uses array
	queueItemPoolPool * searchQueueItemPoolPool
	queuePool * searchQueuePool
}
type Node struct {
	children   [MAX_POSSIBLE_ENTRIES]int
	childrenLength int
	height     int
	isLeaf     bool
	start, end int // index in the underlying array
	BBox       BBox
}

// Create an RTree index from an array of points
func New() *SimpleRTree {
	defaultOptions := Options{
		MAX_ENTRIES: 9,
	}
	return NewWithOptions(defaultOptions)
}

func NewWithOptions(options Options) *SimpleRTree {
	if options.MAX_ENTRIES > MAX_POSSIBLE_ENTRIES {
		log.Fatal("Maximum value for max entries is: ", MAX_POSSIBLE_ENTRIES)
	}
	r := &SimpleRTree{
		options: options,
	}
	return r
}

func (r *SimpleRTree) Load(points FlatPoints) *SimpleRTree {
	return r.load(points, false)
}

func (r *SimpleRTree) LoadSortedArray(points FlatPoints) *SimpleRTree {
	return r.load(points, true)
}

func (r *SimpleRTree) FindNearestPointWithin(x, y, d float64) (x1, y1, d1 float64, found bool) {
	sqd := d * d // we work with squared distances
	return r.findNearestPointWithin(x, y, sqd)
}

func (r *SimpleRTree) FindNearestPoint (x, y float64) (x1, y1, d1 float64, found bool) {
	return r.findNearestPointWithin(x, y, math.Inf(1))
}
func (r *SimpleRTree) findNearestPointWithin (x, y, d float64) (x1, y1, d1 float64, found bool){
	var minItem *searchQueueItem
	distanceLowerBound := math.Inf(1)
	distanceUpperBound := d
	// if bbox is further from this bound then we don't explore it
	sq := r.queuePool.take()
	heap.Init(sq)

	queueItemPool := r.queueItemPoolPool.take()
	rootNode := &r.nodes[0]
	mind, maxd := rootNode.computeDistances(x, y)
	if (maxd < distanceUpperBound) {
		distanceUpperBound = maxd
	}
	// Only start search if it is within bound
	if (mind < distanceUpperBound) {
		item := queueItemPool.take()
		item.node = rootNode
		item.distance = mind
		heap.Push(sq, item)
	}

	for sq.Len() > 0 {
		item := heap.Pop(sq).(*searchQueueItem)
		currentDistance := item.distance
		if (minItem != nil && currentDistance > distanceLowerBound) {
			queueItemPool.giveBack(item);
			break
		}

		if (item.node.isLeaf) {
			// we know it is smaller from the previous test
			distanceLowerBound = currentDistance
			minItem = item
		} else {
			for i := 0; i < item.node.childrenLength; i++ {
				nodeIndex := item.node.children[i]
				n := &r.nodes[nodeIndex]
				mind, maxd := n.computeDistances(x, y)
				if (mind <= distanceUpperBound) {
					childItem := queueItemPool.take()
					childItem.node = n
					childItem.distance = mind
					heap.Push(sq, childItem)
				}
				// Distance to one of the corners is lower than the upper bound
				// so there must be a point at most within distanceUpperBound
				if (maxd < distanceUpperBound) {
					distanceUpperBound = maxd
				}
			}
		}
		queueItemPool.giveBack(item)
	}

	// Return all missing items. This could probably be async
	for sq.Len() > 0 {
		item := heap.Pop(sq).(*searchQueueItem)
		queueItemPool.giveBack(item)
	}

	// return pool of items
	r.queueItemPoolPool.giveBack(queueItemPool)
	r.queuePool.giveBack(sq)

	if (minItem == nil) {
		return
	}
	x1 = minItem.node.BBox.MaxX
	y1 = minItem.node.BBox.MaxY
	// Only do sqrt at the end
	d1 = math.Sqrt(distanceUpperBound)
	found = true
	return
}

func (r *SimpleRTree) load (points FlatPoints, isSorted bool) *SimpleRTree {
	if points.Len() == 0 {
		return r
	}
	if r.built {
		log.Fatal("Tree is static, cannot load twice")
	}
	r.built = true

	r.build(points, isSorted)
	rootNode := r.nodes[0] // TODO handle nil?
	r.queueItemPoolPool = newSearchQueueItemPoolPool(2, rootNode.height * r.options.MAX_ENTRIES)
	r.queuePool = newSearchQueuePool(2, rootNode.height * r.options.MAX_ENTRIES)
	// Max proportion when not checking max distance 2.3111111111111113
	// Max proportion checking max distance 39 6 9 0.7222222222222222
	return r
}

func (r *SimpleRTree) build(points FlatPoints, isSorted bool) {

	r.points = points
	r.nodes = make([]Node, 0, computeSize(points.Len()))
	r.nodes = append(r.nodes, Node{
		height: int(math.Ceil(math.Log(float64(points.Len())) / math.Log(float64(r.options.MAX_ENTRIES)))),
		start: 0,
		end: points.Len(),
	})


	i := 0
	nodesRemaining := 1
	// build while there are non leaf nodes remaining
	for nodesRemaining > 0 {
		nodesRemaining += r.buildNodeDownwards(i, isSorted)
		isSorted = false // Only first one might be sorted
		i++
	}
	r.computeBBoxDownwards(0)
	return
}



func (r *SimpleRTree) buildNodeDownwards(nodeIndex int, isSorted bool) (deltaNodes int) {
	n := &r.nodes[nodeIndex]
	if n.isLeaf {
		return 0
	}
	N := n.end - n.start
	// target number of root entries to maximize storage utilization
	var M float64
	if N <= r.options.MAX_ENTRIES { // Leaf node
		r.setLeafNode(n)
		return -1 // one node processed
	}

	M = math.Ceil(float64(N) / float64(math.Pow(float64(r.options.MAX_ENTRIES), float64(n.height-1))))

	N2 := int(math.Ceil(float64(N) / M))
	N1 := N2 * int(math.Ceil(math.Sqrt(M)))

	// parent node might already be sorted. In that case we avoid double computation
	if (!isSorted) {
		sortX := xSorter{n: n, points: r.points, start: n.start, end: n.end, bucketSize:  N1}
		sortX.Sort()
	}
	for i := 0; i < N; i += N1 {
		right2 := minInt(i+N1, N)
		sortY := ySorter{n: n, points: r.points, start: n.start + i, end: n.start + right2, bucketSize: N2}
		sortY.Sort()
		childIndex := len(r.nodes)
		for j := i; j < right2; j += N2 {
			right3 := minInt(j+N2, right2)
			child := Node{
				start: n.start + j,
				end: n.start + right3,
				height:     n.height - 1,
			}
			r.nodes = append(r.nodes, child)
			n.childrenLength++
			n.children[n.childrenLength - 1] = childIndex
			childIndex++
		}
	}
	return n.childrenLength - 1 // one node processed n.children added
}



// Compute bbox of all tree all the way to the bottom
func (r *SimpleRTree) computeBBoxDownwards(nodeIndex int) BBox {
	n := &r.nodes[nodeIndex]
	var bbox BBox
	if n.isLeaf {
		bbox = n.BBox
	} else {
		bbox = r.computeBBoxDownwards(n.children[0])

		for i := 1; i < n.childrenLength; i++ {
			bbox = bbox.extend(r.computeBBoxDownwards(n.children[i]))
		}
	}
	n.BBox = bbox
	return bbox
}


func (r *SimpleRTree) setLeafNode(n * Node) {
	// Here we follow original rbush implementation.
	n.childrenLength = n.end - n.start
	n.height = 1
	childIndex := len(r.nodes)
	for i := 0; i < n.end - n.start; i++ {
		x1, y1 := r.points.GetPointAt(n.start + i)
		child := Node{
			start: n.start + i,
			end: n.start + i +1,
			isLeaf: true,
			BBox: BBox{
				MinX: x1,
				MaxX: x1,
				MinY: y1,
				MaxY: y1,
			},
		}
		// Note this is not thread safe. At the moment we are doing it in one goroutine so we are safe
		r.nodes = append(r.nodes, child)
		n.children[i] = childIndex
		childIndex++
	}
}

func (r *SimpleRTree) toJSON () {
	text := make([]string, 0)
	fmt.Println(strings.Join(r.toJSONAcc(0, text), ","))
}

func (r *SimpleRTree) toJSONAcc (nodeIndex int, text []string) []string {
	t, err := template.New("foo").Parse(`{
	       "type": "Feature",
	       "properties": {},
	       "geometry": {
       "type": "Polygon",
       "coordinates": [
       [
       [
       {{.BBox.MinX}},
       {{.BBox.MinY}}
       ],
       [
       {{.BBox.MaxX}},
       {{.BBox.MinY}}
       ],
       [
       {{.BBox.MaxX}},
       {{.BBox.MaxY}}
       ],
       [
       {{.BBox.MinX}},
       {{.BBox.MaxY}}
       ],
       [
       {{.BBox.MinX}},
       {{.BBox.MinY}}
       ]
       ]
       ]
       }
       }`)
	if (err != nil) {
		log.Fatal(err)
	}
	var tpl bytes.Buffer
	n := &r.nodes[nodeIndex]
	if err := t.Execute(&tpl, n); err != nil {
		log.Fatal(err)
	}
	text = append(text, tpl.String())
	for i := 0; i < n.childrenLength; i++ {
		c := n.children[i]
		text = r.toJSONAcc(c, text)
	}
	return text
}

func (n * Node) computeDistances (x, y float64) (mind, maxd float64) {
	// TODO try reuse array
	// TODO try simd
	if (n.isLeaf) {
	       // node is point, there is only one distance
	       d := (x - n.BBox.MinX) * (x - n.BBox.MinX)  + (y - n.BBox.MinY) * (y - n.BBox.MinY)
	       return d, d
	}
	minx, maxx := sortFloats((x - n.BBox.MinX) * (x - n.BBox.MinX), (x - n.BBox.MaxX) * (x - n.BBox.MaxX))
	miny, maxy := sortFloats((y - n.BBox.MinY) * (y - n.BBox.MinY), (y - n.BBox.MaxY) * (y - n.BBox.MaxY))

	sideX := (n.BBox.MaxX - n.BBox.MinX) * (n.BBox.MaxX - n.BBox.MinX)
	sideY := (n.BBox.MaxY - n.BBox.MinY) * (n.BBox.MaxY - n.BBox.MinY)

	// fmt.Println(sides)
	// point is inside because max distances in both axis are smaller than sides of the square
	if (maxx < sideX && maxy < sideY) {
		// do nothing mind is already 0
	} else if (maxx < sideX) {
		// point is in vertical stripe. Hence distance to the bbox is maximum vertical distance
		mind = miny
	} else if (maxy < sideY) {
		// point is in horizontal stripe, Hence distance is least distance to one of the sides (vertical distance is 0
		mind = minx
	} else {
		// point is not inside bbox. closest vertex is that one with closest x and y
		mind = minx + miny
	}
	maxd = maxx + maxy
	return
}

func minInt(a, b int) int {
       if a < b {
	       return a
       }
       return b
}


type FlatPoints []float64

func (fp FlatPoints) Len () int {
	return len(fp) / 2
}

func (fp FlatPoints) Swap (i, j int) {
	fp[2 * i], fp[2 * i + 1], fp[2 * j], fp[2 * j + 1] = fp[2 * j], fp[2 * j + 1], fp[2 * i], fp[2 * i + 1]
}

func (fp FlatPoints) GetPointAt(i int) (x1, y1 float64) {
	return fp[2 * i], fp[2 * i +1]
}

func sortFloats (x1, x2 float64) (x3, x4 float64) {
	if (x1 > x2) {
		return x2, x1
	}
	return x1, x2
}

func computeSize (n int) (size int) {
	return 2 * n
}