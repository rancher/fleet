package data

import "github.com/rancher/fleet/internal/cmd/agent/deployer/data/convert"

type List []map[string]interface{}

type Object map[string]interface{}

func (o Object) Map(names ...string) Object {
	v := GetValueN(o, names...)
	m := convert.ToMapInterface(v)
	return m
}

func (o Object) Slice(names ...string) (result []Object) {
	v := GetValueN(o, names...)
	for _, item := range convert.ToInterfaceSlice(v) {
		result = append(result, convert.ToMapInterface(item))
	}
	return
}

func (o Object) String(names ...string) string {
	v := GetValueN(o, names...)
	return convert.ToString(v)
}

func (o Object) StringSlice(names ...string) []string {
	v := GetValueN(o, names...)
	return convert.ToStringSlice(v)
}

func (o Object) Bool(key ...string) bool {
	return convert.ToBool(GetValueN(o, key...))
}
