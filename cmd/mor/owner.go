package main

import (
	"os/user"
	"strconv"

	"mor/internal/fsutil"
)

// adoptDaemonOwner points every file mor writes at the account the daemon runs
// as, whenever the command line is doing the writing as root.
//
// The two halves of mor share one directory: the daemon under its own user,
// the terminal under root. Whoever writes last decides who can read next, and
// a root-owned file in there is invisible to the daemon.
func adoptDaemonOwner() {
	u, err := user.Lookup(daemonUser)
	if err != nil {
		return // no such account: the daemon runs as root, nothing to adopt
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return
	}
	fsutil.SetOwner(uid, gid)
}

// daemonUser is the account the installer creates for the service.
const daemonUser = "mor"
