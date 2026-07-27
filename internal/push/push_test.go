package push

import "testing"

// Both test buttons sit on the same settings page, so the message that arrives
// says which one of them was pressed.
func TestATestMessageNamesTheChannel(t *testing.T) {
	web := TestMessage(TestWebPush)
	if web.Title != "Test notification." {
		t.Fatalf("want the test wording, got %q", web.Title)
	}
	if web.Body != "Web push works." {
		t.Fatalf("want the web push channel named, got %q", web.Body)
	}
	if hook := TestMessage(TestWebhook); hook.Body != "Webhook works." {
		t.Fatalf("want the webhook channel named, got %q", hook.Body)
	}
}
