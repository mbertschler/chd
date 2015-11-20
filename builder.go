package chd

import (
	"bytes"
	"errors"
	"math"
	"math/big"
	"math/rand"
	"sort"
	"time"
)

type item struct {
	key []byte

	// counter is used for
	// removing key duplicates
	counter int

	deleted bool
}

type items []item

func (it items) Len() int {
	return len(it)
}

func (it items) Less(i, j int) bool {
	cmp := bytes.Compare(it[i].key, it[j].key)
	if cmp < 0 {
		return true
	} else if cmp > 0 {
		return false
	}

	// if cmp == 0
	return it[i].counter > it[j].counter
}

func (it items) Swap(i, j int) {
	it[i], it[j] = it[j], it[i]
}

// Builder manages adding
// of items and map creation.
type Builder struct {
	items items

	maxKeySize int
	counter    int
}

type hash struct {
	h1 uint64
	h2 uint64
}

type bucket struct {
	index  uint64
	hashes []hash
}

type buckets []bucket

func (b buckets) Len() int {
	return len(b)
}

func (b buckets) Less(i, j int) bool {
	return len(b[i].hashes) > len(b[j].hashes)
}

func (b buckets) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}

// NewBuilder returns a new map builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// Add adds a given key to the builder.
func (b *Builder) Add(key []byte) {
	item := item{key, b.counter, false}
	b.items = append(b.items, item)

	if len(key) > b.maxKeySize {
		b.maxKeySize = len(key)
	}

	b.counter++
}

// Delete removes the item with the given key.
func (b *Builder) Delete(key []byte) {
	item := item{key, b.counter, true}
	b.items = append(b.items, item)
	b.counter++
}

// Build creates a map given a CompactArray.
// If array is nil, it will use a plain integer
// array instead. Note that array must be gob encodable.
func (b *Builder) Build(array CompactArray) *Map {
	rand.Seed(time.Now().UTC().UnixNano())

	// Sort items in ascending order
	// of keys and decreasing counter
	sort.Sort(b.items)
	items := make(items, 0, len(b.items))

	// Remove duplicates and deleted items
	pkey := randBytes(b.maxKeySize + 1)
	for _, item := range b.items {
		if !bytes.Equal(pkey, item.key) {
			if !item.deleted {
				items = append(items, item)
			}

			pkey = item.key
		}
	}

	bucketSize := 5
	loadFactor := 1.0
	tableSize := int(float64(len(items)) / loadFactor)
	tableSize = nearestPrime(tableSize)

	// Try and try until successful
	for {
		const numTries = 3
		for i := 0; i < numTries; i++ {
			seed := [2]uint64{
				uint64(rand.Int63()),
				uint64(rand.Int63()),
			}

			m, err := b.build(seed, bucketSize, tableSize, items, array)
			if err == nil {
				return m
			}
		}

		// If unsuccessful, reduce the bucket
		// size first and then the load factor
		if bucketSize > 1 {
			bucketSize--
		} else {
			bucketSize = 5
			loadFactor *= 0.90

			tableSize = int(float64(len(items)) / loadFactor)
			tableSize = nearestPrime(tableSize)
		}
	}
}

// build tries to create a map and
// returns an error if unsuccessful.
func (b *Builder) build(
	seed [2]uint64,
	bucketSize,
	tableSize int,
	items []item,
	array CompactArray) (*Map, error) {

	ts := uint64(tableSize)
	nbuckets := uint64(len(items)/bucketSize) + 1
	buckets := make(buckets, nbuckets)
	hashIdx := make([]uint64, nbuckets)

	// Calculate hashes and put them into their designated buckets
	for i := range items {
		h1, h2, h3, _ := spookyHash(items[i].key, seed[0], seed[1])

		h2 %= ts
		h3 %= ts
		hash := hash{h2, h3}

		bidx := h1 % nbuckets
		buckets[bidx].index = bidx
		buckets[bidx].hashes = append(buckets[bidx].hashes, hash)
	}

	// Sort buckets in decreasing size
	sort.Sort(buckets)

	maxHashIdx := ts * ts
	occupied := make([]bool, ts)
	indices := make([]uint64, 0, len(buckets[0].hashes))

	// Process buckets and populate table
	for _, b := range buckets {
		if len(b.hashes) == 0 {
			continue
		}

		d0 := uint64(0)
		d1 := uint64(math.MaxUint64) // rolls back to 0 when 1 is added

		hidx := uint64(0)

	NextHashIdx:
		for {
			if hidx == maxHashIdx {
				return nil, errors.New("chd: can't find collission-free hash function")
			}

			d1++
			if d1 == ts {
				d0++
				d1 = 0
			}

			indices = indices[:0]
			for _, h := range b.hashes {
				idx := (h.h1 + (d0 * h.h2) + d1) % ts

				if occupied[idx] {
					// Collission has occured, clear
					// table of previously added items
					for _, n := range indices {
						occupied[n] = false
					}

					hidx++
					continue NextHashIdx
				}

				occupied[idx] = true
				indices = append(indices, idx)
			}

			hashIdx[b.index] = hidx
			break
		}
	}

	// Construct hash array
	if array == nil {
		array = newIntArray(len(hashIdx))
	}
	for _, idx := range hashIdx {
		array.Add(int(idx))
	}

	m := &Map{
		seed,
		len(items),
		tableSize,
		array,
	}

	return m, nil
}

func nearestPrime(num int) int {
	if num&1 == 0 {
		num++
	}

	for !big.NewInt(int64(num)).ProbablyPrime(10) {
		num += 2
	}

	return num
}

func randBytes(size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(rand.Intn(256))
	}

	return b
}

func keyExists(items items, key []byte) bool {
	n := sort.Search(len(items), func(i int) bool {
		return bytes.Compare(items[i].key, key) >= 0
	})

	if n < len(items) && bytes.Equal(items[n].key, key) {
		return true
	}

	return false
}
