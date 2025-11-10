package monitor

import (
	"reflect"
	"time"

	"github.com/rancher/lasso/pkg/controller"

	"github.com/sirupsen/logrus"
)

type Cond string

var ErrSkip = controller.ErrIgnore

func (c Cond) SetError(obj interface{}, reason string, err error) {
	if err == nil || err == ErrSkip {
		c.True(obj)
		c.Message(obj, "")
		c.Reason(obj, reason)
		return
	}
	if reason == "" {
		reason = "Error"
	}
	c.False(obj)
	c.Message(obj, err.Error())
	c.Reason(obj, reason)
}

func (c Cond) True(obj interface{}) {
	setStatus(obj, string(c), "True")
}

func (c Cond) IsTrue(obj interface{}) bool {
	return getStatus(obj, string(c)) == "True"
}

func (c Cond) False(obj interface{}) {
	setStatus(obj, string(c), "False")
}

func (c Cond) IsFalse(obj interface{}) bool {
	return getStatus(obj, string(c)) == "False"
}

func (c Cond) Reason(obj interface{}, reason string) {
	cond := findOrCreateCond(obj, string(c))
	getFieldValue(cond, "Reason").SetString(reason)
}

func (c Cond) GetReason(obj interface{}) string {
	cond := findOrNotCreateCond(obj, string(c))
	if cond == nil {
		return ""
	}
	return getFieldValue(*cond, "Reason").String()
}

func (c Cond) Message(obj interface{}, message string) {
	cond := findOrCreateCond(obj, string(c))
	setValue(cond, "Message", message)
}

func (c Cond) GetMessage(obj interface{}) string {
	cond := findOrNotCreateCond(obj, string(c))
	if cond == nil {
		return ""
	}
	return getFieldValue(*cond, "Message").String()
}

func touchTS(value reflect.Value) {
	now := time.Now().UTC().Format(time.RFC3339)
	getFieldValue(value, "LastUpdateTime").SetString(now)
}

func getStatus(obj interface{}, condName string) string {
	cond := findOrNotCreateCond(obj, condName)
	if cond == nil {
		return ""
	}
	return getFieldValue(*cond, "Status").String()
}

func setStatus(obj interface{}, condName, status string) {
	if reflect.TypeOf(obj).Kind() != reflect.Ptr {
		panic("obj passed must be a pointer")
	}
	cond := findOrCreateCond(obj, condName)
	setValue(cond, "Status", status)
}

func setValue(cond reflect.Value, fieldName, newValue string) {
	value := getFieldValue(cond, fieldName)
	if value.String() != newValue {
		value.SetString(newValue)
		touchTS(cond)
	}
}

func findOrNotCreateCond(obj interface{}, condName string) *reflect.Value {
	condSlice := getValue(obj, "Status", "Conditions")
	if !condSlice.IsValid() {
		condSlice = getValue(obj, "Conditions")
	}
	return findCond(obj, condSlice, condName)
}

func findOrCreateCond(obj interface{}, condName string) reflect.Value {
	condSlice := getValue(obj, "Status", "Conditions")
	if !condSlice.IsValid() {
		condSlice = getValue(obj, "Conditions")
	}
	cond := findCond(obj, condSlice, condName)
	if cond != nil {
		return *cond
	}

	newCond := reflect.New(condSlice.Type().Elem()).Elem()
	newCond.FieldByName("Type").SetString(condName)
	newCond.FieldByName("Status").SetString("Unknown")
	condSlice.Set(reflect.Append(condSlice, newCond))
	return *findCond(obj, condSlice, condName)
}

func findCond(obj interface{}, val reflect.Value, name string) *reflect.Value {
	defer func() {
		if recover() != nil {
			logrus.Fatalf("failed to find .Status.Conditions field on %v", reflect.TypeOf(obj))
		}
	}()

	for i := 0; i < val.Len(); i++ {
		cond := val.Index(i)
		typeVal := getFieldValue(cond, "Type")
		if typeVal.String() == name {
			return &cond
		}
	}

	return nil
}

func getValue(obj interface{}, name ...string) reflect.Value {
	if obj == nil || len(name) == 0 {
		return reflect.Value{}
	}
	v := reflect.ValueOf(obj)
	t := v.Type()
	if t.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	field := v.FieldByName(name[0])
	if len(name) == 1 {
		return field
	}
	return getFieldValue(field, name[1:]...)
}

func getFieldValue(v reflect.Value, name ...string) reflect.Value {
	if !v.IsValid() {
		return v
	}
	if len(name) == 0 {
		return v
	}
	field := v.FieldByName(name[0])
	if len(name) == 1 {
		return field
	}
	return getFieldValue(field, name[1:]...)
}
