package utils

import (
	"os"
	"reflect"
	"testing"
)

func TestGetEnvAsBool(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envVal   string
		setEnv   bool
		fallback bool
		want     bool
	}{
		{
			name:     "Environment variable exists and is true",
			key:      "TEST_BOOL_TRUE",
			envVal:   "true",
			setEnv:   true,
			fallback: false,
			want:     true,
		},
		{
			name:     "Environment variable exists and is false",
			key:      "TEST_BOOL_FALSE",
			envVal:   "false",
			setEnv:   true,
			fallback: true,
			want:     false,
		},
		{
			name:     "Environment variable exists and is 1",
			key:      "TEST_BOOL_1",
			envVal:   "1",
			setEnv:   true,
			fallback: false,
			want:     true,
		},
		{
			name:     "Environment variable exists and is invalid",
			key:      "TEST_BOOL_INVALID",
			envVal:   "invalid",
			setEnv:   true,
			fallback: true,
			want:     true,
		},
		{
			name:     "Environment variable does not exist",
			key:      "TEST_BOOL_MISSING",
			setEnv:   false,
			fallback: true,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				os.Setenv(tt.key, tt.envVal)
				defer os.Unsetenv(tt.key)
			} else {
				os.Unsetenv(tt.key)
			}

			if got := GetEnvAsBool(tt.key, tt.fallback); got != tt.want {
				t.Errorf("GetEnvAsBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envVal   string
		setEnv   bool
		fallback string
		want     string
	}{
		{
			name:     "Environment variable exists",
			key:      "TEST_STRING_KEY",
			envVal:   "some-value",
			setEnv:   true,
			fallback: "fallback-value",
			want:     "some-value",
		},
		{
			name:     "Environment variable does not exist",
			key:      "TEST_STRING_MISSING",
			setEnv:   false,
			fallback: "fallback-value",
			want:     "fallback-value",
		},
		{
			name:     "Environment variable exists but is empty",
			key:      "TEST_STRING_EMPTY",
			envVal:   "",
			setEnv:   true,
			fallback: "fallback-value",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				os.Setenv(tt.key, tt.envVal)
				defer os.Unsetenv(tt.key)
			} else {
				os.Unsetenv(tt.key)
			}

			if got := GetEnv(tt.key, tt.fallback); got != tt.want {
				t.Errorf("GetEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetEnvAsInt(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envVal   string
		setEnv   bool
		fallback int64
		want     int64
	}{
		{
			name:     "Environment variable exists and is valid int",
			key:      "TEST_INT_VALID",
			envVal:   "123",
			setEnv:   true,
			fallback: 456,
			want:     123,
		},
		{
			name:     "Environment variable exists but is invalid int",
			key:      "TEST_INT_INVALID",
			envVal:   "not-an-int",
			setEnv:   true,
			fallback: 456,
			want:     456,
		},
		{
			name:     "Environment variable does not exist",
			key:      "TEST_INT_MISSING",
			setEnv:   false,
			fallback: 456,
			want:     456,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				os.Setenv(tt.key, tt.envVal)
				defer os.Unsetenv(tt.key)
			} else {
				os.Unsetenv(tt.key)
			}

			if got := GetEnvAsInt(tt.key, tt.fallback); got != tt.want {
				t.Errorf("GetEnvAsInt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		name string
		s    string
		sep  string
		want []string
	}{
		{
			name: "Normal split with spaces",
			s:    "a, b, c",
			sep:  ",",
			want: []string{"a", "b", "c"},
		},
		{
			name: "No spaces to trim",
			s:    "a,b,c",
			sep:  ",",
			want: []string{"a", "b", "c"},
		},
		{
			name: "Extra spaces around",
			s:    "  a  ,  b  ,  c  ",
			sep:  ",",
			want: []string{"a", "b", "c"},
		},
		{
			name: "Empty string",
			s:    "",
			sep:  ",",
			want: []string{""},
		},
		{
			name: "Only separators",
			s:    ",,",
			sep:  ",",
			want: []string{"", "", ""},
		},
		{
			name: "Different separator",
			s:    "one|two|three",
			sep:  "|",
			want: []string{"one", "two", "three"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SplitAndTrim(tt.s, tt.sep); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitAndTrim() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCORSOrigins(t *testing.T) {
	tests := []struct {
		name   string
		envVal string
		setEnv bool
		want   []string
	}{
		{
			name:   "Default value",
			setEnv: false,
			want:   []string{"http://localhost:5173"},
		},
		{
			name:   "Single origin",
			envVal: "https://example.com",
			setEnv: true,
			want:   []string{"https://example.com"},
		},
		{
			name:   "Multiple origins",
			envVal: "https://example.com,http://localhost:3000, https://api.example.com",
			setEnv: true,
			want:   []string{"https://example.com", "http://localhost:3000", "https://api.example.com"},
		},
		{
			name:   "Multiple origins with spaces",
			envVal: "  https://example.com  ,  http://localhost:3000  ",
			setEnv: true,
			want:   []string{"https://example.com", "http://localhost:3000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "CORS_ALLOWED_ORIGINS"
			if tt.setEnv {
				os.Setenv(key, tt.envVal)
				defer os.Unsetenv(key)
			} else {
				os.Unsetenv(key)
			}

			if got := ParseCORSOrigins(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseCORSOrigins() = %v, want %v", got, tt.want)
			}
		})
	}
}
