package cmd

// Copied from https://github.com/rancher/wrangler-cli

import (
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unsafe"

	"github.com/spf13/cobra"
)

var (
	caseRegexp = regexp.MustCompile("([a-z])([A-Z])")
)

type PersistentPreRunnable interface {
	PersistentPre(cmd *cobra.Command, args []string) error
}

type PreRunnable interface {
	Pre(cmd *cobra.Command, args []string) error
}

type HasHelpFunc interface {
	HelpFunc(command *cobra.Command, strings []string)
}

type Runnable interface {
	Run(cmd *cobra.Command, args []string) error
}

type customizer interface {
	Customize(cmd *cobra.Command)
}

type fieldInfo struct {
	FieldType  reflect.StructField
	FieldValue reflect.Value
}

func fields(obj interface{}) []fieldInfo {
	ptrValue := reflect.ValueOf(obj)
	objValue := ptrValue.Elem()

	var result []fieldInfo

	for i := 0; i < objValue.NumField(); i++ {
		fieldType := objValue.Type().Field(i)
		if fieldType.Anonymous && fieldType.Type.Kind() == reflect.Struct {
			result = append(result, fields(objValue.Field(i).Addr().Interface())...)
		} else if !fieldType.Anonymous {
			result = append(result, fieldInfo{
				FieldValue: objValue.Field(i),
				FieldType:  objValue.Type().Field(i),
			})
		}
	}

	return result
}

func Name(obj interface{}) string {
	ptrValue := reflect.ValueOf(obj)
	objValue := ptrValue.Elem()
	commandName := strings.Replace(objValue.Type().Name(), "Command", "", 1)
	commandName, _ = name(commandName, "", "")
	return commandName
}

// Command populates a cobra.Command object by extracting args from struct tags of the
// Runnable obj passed.  Also the Run method is assigned to the RunE of the command.
// name = Override the struct field with

func Command(obj Runnable, cmd cobra.Command) *cobra.Command {
	var (
		envs     []func()
		arrays   = map[string]reflect.Value{}
		slices   = map[string]reflect.Value{}
		maps     = map[string]reflect.Value{}
		ptrValue = reflect.ValueOf(obj)
		objValue = ptrValue.Elem()
	)

	c := cmd
	if c.Use == "" {
		c.Use = Name(obj)
	}

	for _, info := range fields(obj) {
		fieldType := info.FieldType
		v := info.FieldValue

		name, alias := name(fieldType.Name, fieldType.Tag.Get("name"), fieldType.Tag.Get("short"))
		usage := fieldType.Tag.Get("usage")
		env := strings.Split(fieldType.Tag.Get("env"), ",")
		defValue := fieldType.Tag.Get("default")
		if len(env) == 1 && env[0] == "" {
			env = nil
		}
		defInt, err := strconv.Atoi(defValue)
		if err != nil {
			defInt = 0
		}

		flags := c.PersistentFlags()
		switch fieldType.Type.Kind() {
		case reflect.Int:
			flags.IntVarP((*int)(unsafe.Pointer(v.Addr().Pointer())), name, alias, defInt, usage)
		case reflect.String:
			flags.StringVarP((*string)(unsafe.Pointer(v.Addr().Pointer())), name, alias, defValue, usage)
		case reflect.Slice:
			switch fieldType.Tag.Get("split") {
			case "false":
				arrays[name] = v
				flags.StringArrayP(name, alias, nil, usage)
			default:
				slices[name] = v
				flags.StringSliceP(name, alias, nil, usage)
			}
		case reflect.Map:
			maps[name] = v
			flags.StringSliceP(name, alias, nil, usage)
		case reflect.Bool:
			initVal := false
			if defValue == "true" {
				initVal = true
			}
			flags.BoolVarP((*bool)(unsafe.Pointer(v.Addr().Pointer())), name, alias, initVal, usage)
		default:
			panic("Unknown kind on field " + fieldType.Name + " on " + objValue.Type().Name())
		}

		for _, env := range env {
			envs = append(envs, func() {
				v := os.Getenv(env)
				if v != "" {
					fv, err := flags.GetString(name)
					if err == nil && (fv == "" || fv == defValue) {
						_ = flags.Set(name, v)
					}
				}
			})
		}
	}

	if p, ok := obj.(PersistentPreRunnable); ok {
		c.PersistentPreRunE = p.PersistentPre
	}

	if p, ok := obj.(PreRunnable); ok {
		c.PreRunE = p.Pre
	}

	if p, ok := obj.(HasHelpFunc); ok {
		c.SetHelpFunc(p.HelpFunc)
	}

	c.RunE = obj.Run
	c.PersistentPreRunE = bind(c.PersistentPreRunE, arrays, slices, maps, envs)
	c.PreRunE = bind(c.PreRunE, arrays, slices, maps, envs)
	c.RunE = bind(c.RunE, arrays, slices, maps, envs)

	cust, ok := obj.(customizer)
	if ok {
		cust.Customize(&c)
	}

	return &c
}

func assignMaps(app *cobra.Command, maps map[string]reflect.Value) error {
	for k, v := range maps {
		k = contextKey(k)
		s, err := app.Flags().GetStringSlice(k)
		if err != nil {
			return err
		}
		if s != nil {
			values := map[string]string{}
			for _, part := range s {
				parts := strings.SplitN(part, "=", 2)
				if len(parts) == 1 {
					values[parts[0]] = ""
				} else {
					values[parts[0]] = parts[1]
				}
			}
			v.Set(reflect.ValueOf(values))
		}
	}
	return nil
}

func assignSlices(app *cobra.Command, slices map[string]reflect.Value) error {
	for k, v := range slices {
		k = contextKey(k)
		s, err := app.Flags().GetStringSlice(k)
		if err != nil {
			return err
		}
		if s != nil {
			v.Set(reflect.ValueOf(s))
		}
	}
	return nil
}

func assignArrays(app *cobra.Command, arrays map[string]reflect.Value) error {
	for k, v := range arrays {
		k = contextKey(k)
		s, err := app.Flags().GetStringArray(k)
		if err != nil {
			return err
		}
		if s != nil {
			v.Set(reflect.ValueOf(s))
		}
	}
	return nil
}

func contextKey(name string) string {
	parts := strings.Split(name, ",")
	return parts[len(parts)-1]
}

func name(name, setName, short string) (string, string) {
	if setName != "" {
		return setName, short
	}
	parts := strings.Split(name, "_")
	i := len(parts) - 1
	name = caseRegexp.ReplaceAllString(parts[i], "$1-$2")
	name = strings.ToLower(name)
	result := append([]string{name}, parts[0:i]...)
	for i := 0; i < len(result); i++ {
		result[i] = strings.ToLower(result[i])
	}
	if short == "" && len(result) > 1 {
		short = result[1]
	}
	return result[0], short
}

func bind(next func(*cobra.Command, []string) error,
	arrays map[string]reflect.Value,
	slices map[string]reflect.Value,
	maps map[string]reflect.Value,
	envs []func()) func(*cobra.Command, []string) error {
	if next == nil {
		return nil
	}
	return func(cmd *cobra.Command, args []string) error {
		for _, envCallback := range envs {
			envCallback()
		}
		if err := assignArrays(cmd, arrays); err != nil {
			return err
		}
		if err := assignSlices(cmd, slices); err != nil {
			return err
		}
		if err := assignMaps(cmd, maps); err != nil {
			return err
		}

		if next != nil {
			return next(cmd, args)
		}

		return nil
	}
}
