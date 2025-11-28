package cmd

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
)

// simpleRunnable implements only the Runnable interface
type simpleRunnable struct {
	StringField string `name:"string-flag" short:"s" usage:"a string flag" default:"default-value"`
}

func (s *simpleRunnable) Run(_ *cobra.Command, _ []string) error {
	return nil
}

// fullRunnable implements all interfaces
type fullRunnable struct {
	StringField      string            `name:"string-flag" short:"s" usage:"a string flag" default:"default-value"`
	IntField         int               `name:"int-flag" short:"i" usage:"an int flag" default:"42"`
	BoolField        bool              `name:"bool-flag" short:"b" usage:"a bool flag" default:"true"`
	BoolFieldFalse   bool              `name:"bool-flag-false" usage:"a bool flag with false default" default:"false"`
	SliceField       []string          `name:"slice-flag" usage:"a slice flag"`
	ArrayField       []string          `name:"array-flag" usage:"an array flag" split:"false"`
	MapField         map[string]string `name:"map-flag" short:"m" usage:"a map flag"`
	EnvField         string            `name:"env-flag" usage:"an env flag" env:"TEST_ENV_VAR"`
	EnvFieldMultiple string            `name:"env-field-multiple" usage:"an env flag with multiple envs" env:"TEST_ENV_VAR1,TEST_ENV_VAR2"`
}

func (f *fullRunnable) Run(_ *cobra.Command, _ []string) error {
	return nil
}

func (f *fullRunnable) PersistentPre(_ *cobra.Command, _ []string) error {
	return nil
}

func (f *fullRunnable) Pre(_ *cobra.Command, _ []string) error {
	return nil
}

func (f *fullRunnable) HelpFunc(_ *cobra.Command, _ []string) {
}

func (f *fullRunnable) Customize(cmd *cobra.Command) {
	cmd.Short = "customized"
}

// EmbeddedBase is exported for testing embedded struct support
type EmbeddedBase struct {
	BaseField string `name:"base-field" usage:"a base field"`
}

type embeddedRunnable struct {
	EmbeddedBase
	OwnField string `name:"own-field" usage:"own field"`
}

func (e *embeddedRunnable) Run(_ *cobra.Command, _ []string) error {
	return nil
}

// testCommandName is used to test the Name() function
type testCommandName struct{}

func (t *testCommandName) Run(_ *cobra.Command, _ []string) error {
	return nil
}

// CamelCaseCommand tests camelCase to kebab-case conversion
type CamelCaseCommand struct{}

func (c *CamelCaseCommand) Run(_ *cobra.Command, _ []string) error {
	return nil
}

func TestCommand_DefaultUseName(t *testing.T) {
	obj := &simpleRunnable{}
	cmd := Command(obj, cobra.Command{})

	if cmd.Use != "simple-runnable" {
		t.Errorf("expected Use to be 'simple-runnable', got '%s'", cmd.Use)
	}
}

func TestCommand_CustomUseName(t *testing.T) {
	obj := &simpleRunnable{}
	cmd := Command(obj, cobra.Command{Use: "custom-name"})

	if cmd.Use != "custom-name" {
		t.Errorf("expected Use to be 'custom-name', got '%s'", cmd.Use)
	}
}

func TestCommand_StringFlag(t *testing.T) {
	obj := &simpleRunnable{}
	cmd := Command(obj, cobra.Command{})

	flag := cmd.PersistentFlags().Lookup("string-flag")
	if flag == nil {
		t.Fatal("expected 'string-flag' flag to exist")
	}
	if flag.Shorthand != "s" {
		t.Errorf("expected shorthand to be 's', got '%s'", flag.Shorthand)
	}
	if flag.Usage != "a string flag" {
		t.Errorf("expected usage to be 'a string flag', got '%s'", flag.Usage)
	}
	if flag.DefValue != "default-value" {
		t.Errorf("expected default value to be 'default-value', got '%s'", flag.DefValue)
	}
}

func TestCommand_IntFlag(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	flag := cmd.PersistentFlags().Lookup("int-flag")
	if flag == nil {
		t.Fatal("expected 'int-flag' flag to exist")
	}
	if flag.Shorthand != "i" {
		t.Errorf("expected shorthand to be 'i', got '%s'", flag.Shorthand)
	}
	if flag.DefValue != "42" {
		t.Errorf("expected default value to be '42', got '%s'", flag.DefValue)
	}
}

func TestCommand_BoolFlag(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	tests := []struct {
		name     string
		flagName string
		defValue string
	}{
		{"true default", "bool-flag", "true"},
		{"false default", "bool-flag-false", "false"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flag := cmd.PersistentFlags().Lookup(tc.flagName)
			if flag == nil {
				t.Fatalf("expected '%s' flag to exist", tc.flagName)
			}
			if flag.DefValue != tc.defValue {
				t.Errorf("expected default value to be '%s', got '%s'", tc.defValue, flag.DefValue)
			}
		})
	}
}

func TestCommand_SliceFlag(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	flag := cmd.PersistentFlags().Lookup("slice-flag")
	if flag == nil {
		t.Fatal("expected 'slice-flag' flag to exist")
	}
	if flag.Value.Type() != "stringSlice" {
		t.Errorf("expected flag type to be 'stringSlice', got '%s'", flag.Value.Type())
	}
}

func TestCommand_ArrayFlag(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	flag := cmd.PersistentFlags().Lookup("array-flag")
	if flag == nil {
		t.Fatal("expected 'array-flag' flag to exist")
	}
	if flag.Value.Type() != "stringArray" {
		t.Errorf("expected flag type to be 'stringArray', got '%s'", flag.Value.Type())
	}
}

func TestCommand_MapFlag(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	flag := cmd.PersistentFlags().Lookup("map-flag")
	if flag == nil {
		t.Fatal("expected 'map-flag' flag to exist")
	}
	// Maps are implemented as string slices
	if flag.Value.Type() != "stringSlice" {
		t.Errorf("expected flag type to be 'stringSlice', got '%s'", flag.Value.Type())
	}
}

func TestCommand_PersistentPreRunnable(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	if cmd.PersistentPreRunE == nil {
		t.Error("expected PersistentPreRunE to be set")
	}
}

func TestCommand_PreRunnable(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	if cmd.PreRunE == nil {
		t.Error("expected PreRunE to be set")
	}
}

func TestCommand_RunE(t *testing.T) {
	obj := &simpleRunnable{}
	cmd := Command(obj, cobra.Command{})

	if cmd.RunE == nil {
		t.Error("expected RunE to be set")
	}
}

func TestCommand_Customizer(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	if cmd.Short != "customized" {
		t.Errorf("expected Short to be 'customized' (set by Customize), got '%s'", cmd.Short)
	}
}

func TestCommand_EmbeddedStruct(t *testing.T) {
	obj := &embeddedRunnable{}
	cmd := Command(obj, cobra.Command{})

	// Check that the base field from embedded struct is present
	baseFlag := cmd.PersistentFlags().Lookup("base-field")
	if baseFlag == nil {
		t.Fatal("expected 'base-field' flag from embedded struct to exist")
	}

	// Check that the own field is also present
	ownFlag := cmd.PersistentFlags().Lookup("own-field")
	if ownFlag == nil {
		t.Fatal("expected 'own-field' flag to exist")
	}
}

func TestCommand_EnvironmentVariable(t *testing.T) {
	// Clean up environment after test
	originalValue := os.Getenv("TEST_ENV_VAR")
	defer func() {
		if originalValue == "" {
			os.Unsetenv("TEST_ENV_VAR")
		} else {
			os.Setenv("TEST_ENV_VAR", originalValue)
		}
	}()

	// Set environment variable
	os.Setenv("TEST_ENV_VAR", "env-value")

	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	// Execute the command using cobra's Execute which properly initializes flags
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the flag was set from environment
	flag := cmd.PersistentFlags().Lookup("env-flag")
	if flag == nil {
		t.Fatal("expected 'env-flag' flag to exist")
	}
	if flag.Value.String() != "env-value" {
		t.Errorf("expected flag value to be 'env-value', got '%s'", flag.Value.String())
	}
}

func TestCommand_EnvironmentVariableNotOverrideUserValue(t *testing.T) {
	// Clean up environment after test
	originalValue := os.Getenv("TEST_ENV_VAR")
	defer func() {
		if originalValue == "" {
			os.Unsetenv("TEST_ENV_VAR")
		} else {
			os.Setenv("TEST_ENV_VAR", originalValue)
		}
	}()

	// Set environment variable
	os.Setenv("TEST_ENV_VAR", "env-value")

	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	// Set flag value explicitly via args (simulating user input)
	cmd.SetArgs([]string{"--env-flag=user-value"})

	// Execute the command
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the flag was not overridden by environment
	flag := cmd.PersistentFlags().Lookup("env-flag")
	if flag == nil {
		t.Fatal("expected 'env-flag' flag to exist")
	}
	if flag.Value.String() != "user-value" {
		t.Errorf("expected flag value to be 'user-value', got '%s'", flag.Value.String())
	}
}

func TestCommand_NameConversion(t *testing.T) {
	obj := &CamelCaseCommand{}
	cmd := Command(obj, cobra.Command{})

	if cmd.Use != "camel-case" {
		t.Errorf("expected Use to be 'camel-case', got '%s'", cmd.Use)
	}
}

func TestName(t *testing.T) {
	tests := []struct {
		name     string
		obj      interface{}
		expected string
	}{
		{"simple runnable", &simpleRunnable{}, "simple-runnable"},
		{"test command name", &testCommandName{}, "test-name"},
		{"camel case command", &CamelCaseCommand{}, "camel-case"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Name(tc.obj)
			if result != tc.expected {
				t.Errorf("expected Name to be '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestCommand_SliceFieldBinding(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	// Set slice flag values via args
	cmd.SetArgs([]string{"--slice-flag=value1,value2"})

	// Execute the command to trigger binding
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the slice field was populated
	if len(obj.SliceField) != 2 {
		t.Errorf("expected SliceField to have 2 elements, got %d", len(obj.SliceField))
	}
	if len(obj.SliceField) >= 2 && (obj.SliceField[0] != "value1" || obj.SliceField[1] != "value2") {
		t.Errorf("unexpected SliceField values: %v", obj.SliceField)
	}
}

func TestCommand_ArrayFieldBinding(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	// Set array flag values via args (for StringArray, each flag is a separate value)
	cmd.SetArgs([]string{"--array-flag=value1", "--array-flag=value2"})

	// Execute the command to trigger binding
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the array field was populated
	if len(obj.ArrayField) != 2 {
		t.Errorf("expected ArrayField to have 2 elements, got %d", len(obj.ArrayField))
	}
	if len(obj.ArrayField) >= 2 && (obj.ArrayField[0] != "value1" || obj.ArrayField[1] != "value2") {
		t.Errorf("unexpected ArrayField values: %v", obj.ArrayField)
	}
}

func TestCommand_MapFieldBinding(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	// Set map flag values via args
	cmd.SetArgs([]string{"--map-flag=key1=value1", "--map-flag=key2=value2"})

	// Execute the command to trigger binding
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the map field was populated
	if len(obj.MapField) != 2 {
		t.Errorf("expected MapField to have 2 elements, got %d", len(obj.MapField))
	}
	if obj.MapField["key1"] != "value1" || obj.MapField["key2"] != "value2" {
		t.Errorf("unexpected MapField values: %v", obj.MapField)
	}
}

func TestCommand_MapFieldWithNoValue(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	// Set map flag with key only (no value)
	cmd.SetArgs([]string{"--map-flag=key1"})

	// Execute the command to trigger binding
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the map field was populated with empty value
	if len(obj.MapField) != 1 {
		t.Errorf("expected MapField to have 1 element, got %d", len(obj.MapField))
	}
	if obj.MapField["key1"] != "" {
		t.Errorf("expected MapField['key1'] to be empty, got '%s'", obj.MapField["key1"])
	}
}

func TestCommand_IntFieldBinding(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	// Set int flag value via args
	cmd.SetArgs([]string{"--int-flag=100"})

	// Execute the command to trigger binding
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the int field was populated
	if obj.IntField != 100 {
		t.Errorf("expected IntField to be 100, got %d", obj.IntField)
	}
}

func TestCommand_StringFieldBinding(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	// Set string flag value via args
	cmd.SetArgs([]string{"--string-flag=custom-value"})

	// Execute the command to trigger binding
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the string field was populated
	if obj.StringField != "custom-value" {
		t.Errorf("expected StringField to be 'custom-value', got '%s'", obj.StringField)
	}
}

func TestCommand_BoolFieldBinding(t *testing.T) {
	obj := &fullRunnable{}
	cmd := Command(obj, cobra.Command{})

	// Set bool flag value to false via args (it defaults to true)
	cmd.SetArgs([]string{"--bool-flag=false"})

	// Execute the command to trigger binding
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the bool field was populated
	if obj.BoolField != false {
		t.Errorf("expected BoolField to be false, got %v", obj.BoolField)
	}
}

func TestCommand_NoInterfacesImplemented(t *testing.T) {
	// Test that a minimal Runnable works without implementing other interfaces
	obj := &simpleRunnable{}
	cmd := Command(obj, cobra.Command{})

	// PersistentPreRunE should still be set (via bind, but wrapping nil)
	// PreRunE should not be set if Pre interface is not implemented
	// RunE should be set
	if cmd.RunE == nil {
		t.Error("expected RunE to be set")
	}
}

// unsupportedFieldRunnable has an unsupported field type (float64)
type unsupportedFieldRunnable struct {
	UnsupportedField float64 `name:"unsupported" usage:"this should panic"`
}

func (u *unsupportedFieldRunnable) Run(_ *cobra.Command, _ []string) error {
	return nil
}

func TestCommand_UnsupportedFieldTypePanics(t *testing.T) {
	obj := &unsupportedFieldRunnable{}

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unsupported field type")
		}
	}()

	Command(obj, cobra.Command{})
}

// fieldWithNoTagsRunnable tests that fields without tags still work
type fieldWithNoTagsRunnable struct {
	FieldWithoutTags string
}

func (f *fieldWithNoTagsRunnable) Run(_ *cobra.Command, _ []string) error {
	return nil
}

func TestCommand_FieldWithoutTags(t *testing.T) {
	obj := &fieldWithNoTagsRunnable{}
	cmd := Command(obj, cobra.Command{})

	// The field should be converted to kebab-case flag name
	flag := cmd.PersistentFlags().Lookup("field-without-tags")
	if flag == nil {
		t.Fatal("expected 'field-without-tags' flag to exist")
	}
}

// runnableWithIntNonNumericDefault tests int fields with non-numeric defaults
type runnableWithIntNonNumericDefault struct {
	IntField int `name:"int-field" default:"not-a-number"`
}

func (r *runnableWithIntNonNumericDefault) Run(_ *cobra.Command, _ []string) error {
	return nil
}

func TestCommand_IntFieldNonNumericDefault(t *testing.T) {
	obj := &runnableWithIntNonNumericDefault{}
	cmd := Command(obj, cobra.Command{})

	flag := cmd.PersistentFlags().Lookup("int-field")
	if flag == nil {
		t.Fatal("expected 'int-field' flag to exist")
	}
	// Non-numeric default should result in 0
	if flag.DefValue != "0" {
		t.Errorf("expected default value to be '0' for non-numeric default, got '%s'", flag.DefValue)
	}
}
