package linkstate

import "testing"

func TestManagedAndRevokedLinks(t *testing.T) {
	Replace(map[int]int64{42: 200, 43: -200})
	t.Cleanup(func() { Replace(nil) })
	if !Allows(42, 200) || Allows(42, 199) {
		t.Fatal("managed link did not enforce its active expiry")
	}
	if Allows(43, 200) {
		t.Fatal("revoked link should reject its previous expiry")
	}
	if !Allows(99, 123) {
		t.Fatal("legacy unmanaged links should remain valid")
	}
}
