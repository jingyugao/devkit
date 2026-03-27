package importer

import (
	"testing"

	"github.com/jingyugao/devkit/internal/authrun/profile"
	"github.com/jingyugao/devkit/internal/authrun/store"
)

func TestImportMySQLParsesLoginPathOutput(t *testing.T) {
	p, secret, err := ImportMySQL(MySQLInput{
		Name:      "doris",
		LoginPath: "doris",
		Database:  "analytics",
	}, func(name string, args ...string) ([]byte, error) {
		if name != "mysql_config_editor" {
			t.Fatalf("unexpected command: %q", name)
		}
		return []byte("[doris]\nuser = root\nhost = 127.0.0.1\nport = 9030\nsocket = /tmp/mysql.sock\npassword = *****\n"), nil
	})
	if err != nil {
		t.Fatalf("ImportMySQL returned error: %v", err)
	}
	if p.Type != profile.TypeMySQL || p.MySQLLoginPath != "doris" || p.Username != "root" || p.Host != "127.0.0.1" || p.Port != 9030 || p.Socket != "/tmp/mysql.sock" || p.Database != "analytics" {
		t.Fatalf("unexpected mysql profile: %#v", p)
	}
	if secret != (store.Secret{}) {
		t.Fatalf("expected empty mysql secret, got %#v", secret)
	}
}
