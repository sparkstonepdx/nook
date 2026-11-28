package web

import "testing"

func TestUsersMerge(t *testing.T) {
	a, _ := OpenStore(t.TempDir())
	b, _ := OpenStore(t.TempDir())
	swap := func() { a.Merge(b.Export()); b.Merge(a.Export()) }

	a.SetPassword("adam", "correct horse battery 1", "")
	b.SetPassword("sam", "correct horse battery 2", "")
	swap()
	if !a.CheckPassword("sam", "correct horse battery 2") || !b.CheckPassword("adam", "correct horse battery 1") {
		t.Fatal("logins didn't cross")
	}

	// the newer change wins, either way round
	b.SetPassword("adam", "correct horse battery 3", "")
	swap()
	if !a.CheckPassword("adam", "correct horse battery 3") {
		t.Fatal("a newer password didn't win")
	}
	a.SetAccess("sam", Member, []string{"web"})
	swap()
	if u := b.Users()["sam"]; u.Role != Member || len(u.Apps) != 1 {
		t.Fatalf("access didn't cross: %+v", u)
	}

	// a removal crosses, and an older copy doesn't bring the login back
	old := b.Export()
	a.Remove("sam")
	swap()
	if _, ok := b.Users()["sam"]; ok {
		t.Fatal("a removal didn't cross")
	}
	a.Merge(old)
	if _, ok := a.Users()["sam"]; ok {
		t.Fatal("an older copy brought a removed login back")
	}
	// inviting the name again later works, and crosses
	a.SetPassword("sam", "correct horse battery 4", "")
	swap()
	if !b.CheckPassword("sam", "correct horse battery 4") {
		t.Fatal("a login made again after removal didn't cross")
	}

	// logins from before linking (no date) are left as each side has them: separate, never lost
	c, _ := OpenStore(t.TempDir())
	m := c.users()
	m["adam"] = User{Salt: "00", Hash: "ff", Gen: 1}
	c.saveUsers(m)
	c.Merge(UsersExport{Users: map[string]User{"adam": {Salt: "11", Hash: "ee", Gen: 1}}})
	if c.Users()["adam"].Hash != "ff" {
		t.Fatal("an undated login was replaced")
	}
	// bad names don't come in
	c.Merge(UsersExport{Users: map[string]User{"../x": {Hash: "ee", Updated: stamp()}}})
	if _, ok := c.Users()["../x"]; ok {
		t.Fatal("a bad name came in")
	}
}
