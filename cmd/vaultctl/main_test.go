package main

import "testing"

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		0:              "0 B",
		999:            "999 B",
		1000:           "1.0 KB",
		8001563222016:  "8.0 TB",
		512110190592:   "512 GB",
		20000000000000: "20.0 TB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestNotYetCommandsChangeNothing(t *testing.T) {
	for _, args := range [][]string{{"upload"}, {"download"}, {"users"}, {"share", "x"}} {
		if code := realMain(args); code != 3 {
			t.Errorf("%v exit = %d, want 3", args, code)
		}
	}
	if code := realMain([]string{"bogus"}); code != 2 {
		t.Errorf("unknown command exit = %d", code)
	}
}
