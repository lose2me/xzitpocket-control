package cloud

import "testing"

// The expected values below were computed independently from the documented
// Tencent algorithms, so a change in the signing code is caught here.

func TestCosAuthorizationMatchesKnownSignature(t *testing.T) {
	auth := cosAuthorization(
		"AKIDtest",
		"secretkey",
		"1700000000;1700003600",
		"put",
		"/books/x/original.pdf",
		"content-type;host",
		"content-type=application%2Fpdf&host=bucket.cos.ap-shanghai.myqcloud.com",
	)
	const want = "q-sign-algorithm=sha1" +
		"&q-ak=AKIDtest" +
		"&q-sign-time=1700000000;1700003600" +
		"&q-key-time=1700000000;1700003600" +
		"&q-header-list=content-type;host" +
		"&q-url-param-list=" +
		"&q-signature=743ab9532d9008f5834cdb943fbf1d55e6571445"
	if auth != want {
		t.Fatalf("cosAuthorization mismatch:\n got %q\nwant %q", auth, want)
	}
}

func TestCdnSignMatchesKnownValue(t *testing.T) {
	if got := cdnSign("testcdnkey", "/books/x/original.pdf", 1700000000); got != "433522f3de0c4a5ecd0cec74270be90c" {
		t.Fatalf("cdnSign = %q, want 433522f3de0c4a5ecd0cec74270be90c", got)
	}
}
