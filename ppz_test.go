package main

import "testing"

// roomPipe must always emit a valid ppz segment:
// ^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$ (max 32, no trailing dash).
func TestRoomPipe(t *testing.T) {
	cases := map[string]string{
		"pixel-studios": "room-pixel-studios",
		"Game Website":  "room-game-website",
		"My_Repo.v2":    "room-my-repo-v2",
		"--weird--":     "room-weird",
		"a-very-long-project-name-over-the-limit": "room-a-very-long-project-name-ov",
	}
	for in, want := range cases {
		if got := roomPipe(in); got != want {
			t.Errorf("roomPipe(%q) = %q, want %q", in, got, want)
		}
	}
}
