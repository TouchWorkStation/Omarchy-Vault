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
	for _, args := range [][]string{{"download"}, {"share", "x"}} {
		if code := realMain(args); code != 3 {
			t.Errorf("%v exit = %d, want 3", args, code)
		}
	}
	if code := realMain([]string{"bogus"}); code != 2 {
		t.Errorf("unknown command exit = %d", code)
	}
}

func TestParseFolders(t *testing.T) {
	got, err := parseFolders("Photos, Documents:ro,Shared:rw", "family")
	if err != nil || len(got) != 3 || got[1].Access != "ro" || got[2].Access != "rw" {
		t.Fatalf("%+v %v", got, err)
	}
	g, _ := parseFolders("Shared:rw", "guest")
	if g[0].Access != "ro" {
		t.Error("guests are always read-only")
	}
	if _, err := parseFolders("../etc", "family"); err == nil {
		t.Error("bad folder accepted")
	}
	if _, err := parseFolders("Photos:all", "family"); err == nil {
		t.Error("bad access accepted")
	}
}
