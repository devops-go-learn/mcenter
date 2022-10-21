package impl_test

import (
	"context"
	"testing"

	"github.com/infraboard/mcenter/apps/domain"
	"github.com/infraboard/mcenter/apps/user"
	"github.com/infraboard/mcenter/test/tools"
	"github.com/infraboard/mcube/app"
)

var (
	impl user.Service
	ctx  = context.Background()
)

func TestCreateUser(t *testing.T) {
	req := user.NewCreateUserRequest()
	req.Domain = domain.DEFAULT_DOMAIN
	req.Username = "admin"
	req.Password = "123456"
	req.Type = user.TYPE_SUPPER
	r, err := impl.CreateUser(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(r)
}

func init() {
	tools.DevelopmentSetup()
	impl = app.GetInternalApp(user.AppName).(user.Service)
}
