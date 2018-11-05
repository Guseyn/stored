package stored

import (
	"fmt"
	"reflect"

	"github.com/apple/foundationdb/bindings/go/src/fdb"
	"github.com/apple/foundationdb/bindings/go/src/fdb/subspace"
)

type valueRaw map[string][]byte

type Value struct {
	object *Object
	fetch  func()
	data   map[string]interface{}
	err    error
}

func (v *Value) get() {
	if v.fetch != nil {
		v.fetch()
		v.fetch = nil
	}
}

func (v *Value) fromRaw(raw valueRaw) {
	v.data = map[string]interface{}{}
	for fieldName, binaryValue := range raw {
		field, ok := v.object.fields[fieldName]

		if !ok {
			continue
		}
		v.data[fieldName] = field.ToInterface(binaryValue)
	}
}

// FromKeyValue pasrses key value from foundationdb
func (v *Value) FromKeyValue(sub subspace.Subspace, rows []fdb.KeyValue) {
	v.data = map[string]interface{}{}
	for _, row := range rows {
		key, err := sub.Unpack(row.Key)
		//key, err := tuple.Unpack(row.Key)
		if err != nil || len(key) < 1 {
			fmt.Println("key in invalid", err)
			continue
		}

		fieldName, ok := key[0].(string)
		if !ok {
			fmt.Println("field is not string")
			continue
		}
		field, ok := v.object.fields[fieldName]

		if !ok {
			fmt.Println("SKIP FIELD", fieldName)
			continue
		}

		//fmt.Println("kv get:", fieldName, row.Value, field.ToInterface(row.Value))
		v.data[fieldName] = field.ToInterface(row.Value)
	}
}

// Scan fills object with data from value
func (v *Value) Scan(obj interface{}) error {
	if v.fetch != nil {
		v.fetch()
		v.fetch = nil
	}
	if v.err != nil {
		return v.err
	}
	object := reflect.ValueOf(obj).Elem()
	for key, val := range v.data {
		field, ok := v.object.fields[key]
		if !ok {
			continue
		}
		objField := object.Field(field.Num)
		if !objField.CanSet() {
			fmt.Println("Could not set object", key)
			continue
		}

		interfaceValue := reflect.ValueOf(val)

		objField.Set(interfaceValue)
	}
	return nil
}

// Reflect returns link to reflact value of object
func (v *Value) Reflect() (reflect.Value, error) {
	if v.fetch != nil {
		v.fetch()
		v.fetch = nil
	}
	value := reflect.New(v.object.reflectType)
	if v.err != nil {
		return value, v.err
	}
	value = value.Elem()
	for key, val := range v.data {
		field, ok := v.object.fields[key]
		if !ok {
			continue
		}
		objField := value.Field(field.Num)
		if !objField.CanSet() {
			fmt.Println("Could not set object", key)
			continue
		}
		interfaceValue := reflect.ValueOf(val)
		objField.Set(interfaceValue)
	}
	return value, nil
}

// Err returns an error
func (v *Value) Err() error {
	if v.fetch != nil {
		v.fetch()
		v.fetch = nil
	}
	return v.err
}

// Interface returns an interface
func (v *Value) Interface() interface{} {
	refl, _ := v.Reflect()
	return refl.Interface()
}
