package gate

import "testing"

func TestGate(t *testing.T) {
	ok := []string{
		"rsync --server -logDtpre.iLsfxCIvu --delete . builds/myapp/",
		"rsync --server -logDtpre.iLsfxCIvu --delete . builds/pr-12.client.com/",
		"app site deploy builds/myapp/ myapp",
		"app site rm myapp-pr-3",
	}
	bad := []string{
		"", "bash", "sh -c id", "app create x", "app site deploy x; id",
		"rsync --server --sender -logDtpre.iLsfxCIvu . builds/x/",
		"rsync --server -logDtpre.iLsfxCIvu . /etc/",
		"rsync --server -logDtpre.iLsfxCIvu . builds/../.ssh/",
		"rsync --server -logDtpre.iLsfxCIvu --log-file=/tmp/x . builds/x/",
		"rsync --server -logDtpre.iLsfxCIvu . data/",
		"rsync --server -T /tmp . builds/x/",
		"cat $HOME/secrets.env",
	}
	for _, c := range ok {
		a, err := Split(c)
		if err == nil {
			_, err = Check(a)
		}
		if err != nil {
			t.Errorf("should allow %q: %v", c, err)
		}
	}
	for _, c := range bad {
		a, err := Split(c)
		if err == nil {
			_, err = Check(a)
		}
		if err == nil {
			t.Errorf("should refuse %q", c)
		}
	}
}
