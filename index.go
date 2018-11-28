package stored

import (
	"bytes"
	"errors"
	"reflect"

	"github.com/apple/foundationdb/bindings/go/src/fdb"
	"github.com/apple/foundationdb/bindings/go/src/fdb/directory"
	"github.com/apple/foundationdb/bindings/go/src/fdb/subspace"
	"github.com/apple/foundationdb/bindings/go/src/fdb/tuple"
	"github.com/mmcloughlin/geohash"
)

// Index represend all indexes sored has
type Index struct {
	Name      string
	Unique    bool
	Geo       int // geo precision used to
	dir       directory.DirectorySubspace
	object    *Object
	field     *Field
	secondary *Field
	handle    func(interface{}) Key
}

// getKey will return index tuple
func (i *Index) getKey(input *Struct) (key tuple.Tuple) {
	if i.handle != nil {
		keyBytes := i.handle(input.object.Interface())
		// Would not index object if key is empty
		if keyBytes == nil || len(keyBytes) == 0 {
			return nil
		}
		key = tuple.Tuple{keyBytes}
	} else {
		indexValue := input.Get(i.field)
		if i.field.isEmpty(indexValue) {
			return nil
		}
		if i.Geo != 0 {
			lngInterface := input.Get(i.secondary)
			lat, long := indexValue.(float64), lngInterface.(float64)
			if lat == 0.0 && long == 0.0 {
				return nil
			}
			hash := geohash.Encode(lat, long)
			if i.Geo < 12 {
				hash = hash[0:i.Geo] // Cutting hash to needed precision
			}
			key = tuple.Tuple{hash}
		} else {
			key = tuple.Tuple{indexValue}
		}
	}
	return
}

// Write writes index related keys
func (i *Index) Write(tr fdb.Transaction, primaryTuple tuple.Tuple, input, oldObject *Struct) error {
	key := i.getKey(input)
	if oldObject != nil {
		toDelete := i.getKey(oldObject)
		if toDelete != nil {
			if reflect.DeepEqual(toDelete, key) {
				return nil
			}
			i.Delete(tr, primaryTuple, toDelete)
		}
	}
	// nil means should not index this object
	if key == nil {
		return nil
	}

	if i.Unique {
		previousPromise := tr.Get(i.dir.Pack(key))

		tr.Set(i.dir.Pack(key), primaryTuple.Pack()) // will be cancelled in case of error

		previousBytes, err := previousPromise.Get()
		if err != nil {
			return err
		}
		if len(previousBytes) != 0 {
			if !bytes.Equal(primaryTuple.Pack(), previousBytes) {
				return ErrAlreadyExist
			}
		}
	} else {
		fullKey := append(key, primaryTuple...)
		tr.Set(i.dir.Pack(fullKey), []byte{})
	}
	return nil
}

// Delete removes selected index
func (i *Index) Delete(tr fdb.Transaction, primaryTuple tuple.Tuple, key tuple.Tuple) {
	if key == nil {
		// no need to clean, this field wasn't indexed
		return
	}
	sub := i.dir.Sub(key...)
	if i.Unique {
		tr.Clear(sub)
	} else {
		// Add primary here
		sub = sub.Sub(primaryTuple...)
		start, end := sub.FDBRangeKeys()
		tr.ClearRange(fdb.KeyRange{Begin: start, End: end})
	}
}

func (i *Index) getList(tr fdb.ReadTransaction, q *Query) ([]*needObject, error) {
	if i.Unique {
		i.object.panic("index is unique (lists not supported)")
	}
	sub := i.dir.Sub(q.primary...)
	start, end := sub.FDBRangeKeys()
	if q.from != nil {
		//start = sub.Sub(q.from...)
		if q.reverse {
			end = sub.Pack(q.from)
		} else {
			start = sub.Pack(q.from)
		}
	}

	r := fdb.KeyRange{Begin: start, End: end}
	rangeResult := tr.GetRange(r, fdb.RangeOptions{Mode: fdb.StreamingModeWantAll, Limit: q.limit, Reverse: q.reverse})
	iterator := rangeResult.Iterator()

	primaryLen := len(i.object.primaryFields)
	values := []*needObject{}
	for iterator.Advance() {
		kv, err := iterator.Get()
		if err != nil {
			return nil, err
		}
		fullTuple, err := sub.Unpack(kv.Key)
		if err != nil {
			return nil, err
		}
		if len(fullTuple)-primaryLen < 0 {
			return nil, errors.New("invalid data: key too short")
		}
		key := fullTuple[len(fullTuple)-primaryLen:]

		values = append(values, i.object.need(tr, i.object.sub(key)))
	}
	return values, nil
}

func (i *Index) getPrimary(tr fdb.ReadTransaction, data interface{}) (subspace.Subspace, error) {
	sub := i.dir.Sub(data)
	if i.Unique {
		bytes, err := tr.Get(sub).Get()
		if err != nil {
			return nil, err
		}
		if len(bytes) == 0 {
			return nil, ErrNotFound
		}
		primaryTuple, err := tuple.Unpack(bytes)
		if err != nil {
			return nil, err
		}
		return i.object.primary.Sub(primaryTuple...), nil
	} else {
		sel := fdb.FirstGreaterThan(sub)
		primaryKey, err := tr.GetKey(sel).Get()
		if err != nil {
			return nil, err
		}
		primaryTuple, err := sub.Unpack(primaryKey)
		//primary, err := UnpackKeyIndex(indexKey, primaryKey)
		if err != nil || len(primaryTuple) < 1 {
			return nil, ErrNotFound
		}

		return i.object.primary.Sub(primaryTuple...), nil
	}
}

// ReindexUnsafe will update index info (NOT consistency safe function)
// this function will use data provited by th object so should be used with care
func (i *Index) ReindexUnsafe(data interface{}) *PromiseErr {
	input := StructAny(data)
	p := i.object.promiseErr()
	p.do(func() Chain {
		primaryTuple := input.Primary(i.object)
		err := i.Write(p.tr, primaryTuple, input, nil)
		if err != nil {
			return p.fail(err)
		}
		return p.done(nil)
	})
	return p
}
