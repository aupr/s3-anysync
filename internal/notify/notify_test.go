package notify

import "testing"

func TestRecorderAndNop(t *testing.T) {
	var n Notifier = &Recorder{}
	n.Notify("s3-anysync", "conflict in Editor")
	n.Notify("s3-anysync", "Editor failed 3 times")
	if got := n.(*Recorder).Messages(); len(got) != 2 || got[0] != "s3-anysync: conflict in Editor" {
		t.Fatalf("messages = %v", got)
	}
	if err := (Nop{}).Notify("a", "b"); err != nil {
		t.Fatal(err)
	}
	var _ Notifier = NewDesktop()
}
