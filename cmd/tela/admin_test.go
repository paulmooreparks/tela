package main

import (
	"flag"
	"reflect"
	"testing"
)

// makeFS builds a FlagSet with a representative mix of string and bool
// flags for permuteArgs tests. The string vars match real admin
// commands' flag names so the tests double as a smoke test on the real
// admin flag schema.
func makeFS() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("hub", "", "Hub URL")
	fs.String("token", "", "Auth token")
	fs.String("machine", "", "Machine ID")
	fs.String("role", "", "Role")
	fs.Int("n", 0, "Count")
	fs.Bool("verbose", false, "Verbose")
	fs.Bool("json", false, "JSON output")
	return fs
}

func TestPermuteArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
		{
			name: "all positional",
			in:   []string{"alice", "barn", "connect,manage"},
			want: []string{"alice", "barn", "connect,manage"},
		},
		{
			name: "all flags first already",
			in:   []string{"-hub", "myhub", "-token", "tok", "alice"},
			want: []string{"-hub", "myhub", "-token", "tok", "alice"},
		},
		{
			name: "flags after positional get hoisted",
			in:   []string{"alice", "-hub", "myhub", "-token", "tok"},
			want: []string{"-hub", "myhub", "-token", "tok", "alice"},
		},
		{
			name: "interleaved with multiple positionals",
			in:   []string{"alice", "barn", "-hub", "myhub", "connect,manage", "-token", "tok"},
			want: []string{"-hub", "myhub", "-token", "tok", "alice", "barn", "connect,manage"},
		},
		{
			name: "flag=value form",
			in:   []string{"alice", "-hub=myhub", "-token=tok"},
			want: []string{"-hub=myhub", "-token=tok", "alice"},
		},
		{
			name: "bool flag does not consume next arg",
			in:   []string{"alice", "-verbose", "barn"},
			want: []string{"-verbose", "alice", "barn"},
		},
		{
			name: "bool flag mixed with value flag",
			in:   []string{"alice", "-verbose", "-hub", "myhub"},
			want: []string{"-verbose", "-hub", "myhub", "alice"},
		},
		{
			name: "double-dash terminator preserves everything after",
			in:   []string{"-hub", "myhub", "--", "-not-a-flag", "literal"},
			want: []string{"-hub", "myhub", "-not-a-flag", "literal"},
		},
		{
			name: "int flag value",
			in:   []string{"-n", "100", "barn"},
			want: []string{"-n", "100", "barn"},
		},
		{
			name: "int flag value after positional",
			in:   []string{"barn", "-n", "100"},
			want: []string{"-n", "100", "barn"},
		},
		{
			name: "unknown flag is treated as value-bearing",
			in:   []string{"alice", "-unknown", "value", "barn"},
			want: []string{"-unknown", "value", "alice", "barn"},
		},
		{
			name: "real-world: tela admin access grant",
			in:   []string{"alice", "barn", "connect,manage", "-hub", "myhub", "-token", "owner-tok"},
			want: []string{"-hub", "myhub", "-token", "owner-tok", "alice", "barn", "connect,manage"},
		},
		{
			name: "real-world: tela admin tokens add with role",
			in:   []string{"bob", "-role", "admin", "-hub", "myhub"},
			want: []string{"-role", "admin", "-hub", "myhub", "bob"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := makeFS()
			got := permuteArgs(fs, c.in)
			// Treat nil and empty slice as equivalent for the empty case.
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("permuteArgs(%v)\n  got  %v\n  want %v", c.in, got, c.want)
			}
		})
	}
}

// permuteArgs's output should be parseable by the same FlagSet without
// errors. This catches subtle bugs where a flag and its value get split
// across the flag/positional boundary.
func TestPermuteArgs_OutputParsesCleanly(t *testing.T) {
	cases := [][]string{
		{"alice", "barn", "-hub", "myhub", "-token", "tok"},
		{"-verbose", "alice"},
		{"alice", "-n", "100"},
		{"alice", "-role", "admin", "-hub", "myhub"},
	}
	for _, args := range cases {
		fs := makeFS()
		permuted := permuteArgs(fs, args)
		if err := fs.Parse(permuted); err != nil {
			t.Errorf("permuteArgs(%v) -> %v failed to parse: %v", args, permuted, err)
		}
	}
}
