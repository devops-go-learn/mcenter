package service

import (
	context "context"
	"fmt"
	"hash/fnv"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/imdario/mergo"
	request "github.com/infraboard/mcube/http/request"
	pb_request "github.com/infraboard/mcube/pb/request"
	"github.com/rs/xid"
	"google.golang.org/grpc/metadata"

	"github.com/infraboard/mcenter/apps/domain"
	"github.com/infraboard/mcenter/apps/namespace"
	"github.com/infraboard/mcenter/apps/token"
)

const (
	AppName = "service"
)

const (
	ClientHeaderKey = "client-id"
	ClientSecretKey = "client-secret"
)

var (
	validate = validator.New()
)

type MetaService interface {
	CreateService(context.Context, *CreateServiceRequest) (*Service, error)
	UpdateService(context.Context, *UpdateServiceRequest) (*Service, error)
	DeleteService(context.Context, *DeleteServiceRequest) (*Service, error)
	RefreshCredential(context.Context, *DescribeServiceRequest) (*Service, error)
	RPCServer
}

func NewCreateServiceRequest() *CreateServiceRequest {
	return &CreateServiceRequest{
		Domain:     domain.DEFAULT_DOMAIN,
		Namespace:  namespace.DEFAULT_NAMESPACE,
		Enabled:    true,
		Repository: &Repository{},
		Tags:       map[string]string{},
	}
}

func NewService(req *CreateServiceRequest) (*Service, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	app := &Service{
		Id:         xid.New().String(),
		CreateAt:   time.Now().UnixMilli(),
		Spec:       req,
		Credential: NewRandomCredential(),
		Security:   NewRandomSecurity(),
	}
	app.Id = app.FullNameHash()
	return app, nil
}

func NewRandomCredential() *Credential {
	return &Credential{
		ClientId:     token.MakeBearer(24),
		ClientSecret: token.MakeBearer(32),
	}
}

func NewRandomSecurity() *Security {
	return &Security{
		EncryptKey: token.MakeBearer(64),
	}
}

func NewValidateCredentialRequest(clientId, clientSercet string) *ValidateCredentialRequest {
	return &ValidateCredentialRequest{
		ClientId:     clientId,
		ClientSecret: clientSercet,
	}
}

func (req *CreateServiceRequest) Validate() error {
	return validate.Struct(req)
}

func NewServiceSet() *ServiceSet {
	return &ServiceSet{
		Items: []*Service{},
	}
}

func (s *ServiceSet) Add(item *Service) {
	s.Items = append(s.Items, item)
}

func NewDefaultService() *Service {
	return &Service{
		Spec: &CreateServiceRequest{},
	}
}

func NewDescribeServiceRequest(id string) *DescribeServiceRequest {
	return &DescribeServiceRequest{
		Id: id,
	}
}

func NewQueryServiceRequest() *QueryServiceRequest {
	return &QueryServiceRequest{
		Page: request.NewDefaultPageRequest(),
	}
}

func NewQueryServiceRequestFromHTTP(r *http.Request) *QueryServiceRequest {
	return &QueryServiceRequest{
		Page: request.NewPageRequestFromHTTP(r),
	}
}

func NewDeleteServiceRequestWithID(id string) *DeleteServiceRequest {
	return &DeleteServiceRequest{
		Id: id,
	}
}

func (i *Service) FullNameHash() string {
	hash := fnv.New32a()
	hash.Write([]byte(i.FullName()))
	return fmt.Sprintf("%x", hash.Sum32())
}

func (i *Service) FullName() string {
	return fmt.Sprintf("%s.%s", i.Spec.Namespace, i.Spec.Name)
}

func (i *Service) Update(req *UpdateServiceRequest) {
	i.UpdateAt = time.Now().UnixMilli()
	i.UpdateBy = req.UpdateBy
	i.Spec = req.Spec
}

func (i *Service) Patch(req *UpdateServiceRequest) error {
	i.UpdateAt = time.Now().UnixMicro()
	i.UpdateBy = req.UpdateBy
	return mergo.MergeWithOverwrite(i.Spec, req.Spec)
}

func NewUpdateServiceRequest(id string) *UpdateServiceRequest {
	return &UpdateServiceRequest{
		Id:         id,
		UpdateMode: pb_request.UpdateMode_PUT,
		UpdateAt:   time.Now().UnixMilli(),
		Spec:       NewCreateServiceRequest(),
	}
}

func NewPutServiceRequest(id string) *UpdateServiceRequest {
	return &UpdateServiceRequest{
		Id:         id,
		UpdateMode: pb_request.UpdateMode_PUT,
		UpdateAt:   time.Now().UnixMilli(),
		Spec:       NewCreateServiceRequest(),
	}
}

func NewPatchServiceRequest(id string) *UpdateServiceRequest {
	return &UpdateServiceRequest{
		Id:         id,
		UpdateMode: pb_request.UpdateMode_PATCH,
		UpdateAt:   time.Now().UnixMilli(),
		Spec:       NewCreateServiceRequest(),
	}
}

func NewDescribeServiceRequestByClientId(clientId string) *DescribeServiceRequest {
	return &DescribeServiceRequest{
		DescribeBy: DescribeBy_SERVICE_CLIENT_ID,
		ClientId:   clientId,
	}
}

func (c *Credential) Validate(clientSecret string) error {
	if c.ClientSecret != clientSecret {
		return fmt.Errorf("client_id or client_secret is not conrrect")
	}

	return nil
}

func GetClientCredential(ctx context.Context) (clientId, clientSecret string) {
	// 重上下文中获取认证信息
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return
	}

	cids := md.Get(ClientHeaderKey)
	sids := md.Get(ClientSecretKey)
	if len(cids) > 0 {
		clientId = cids[0]
	}
	if len(sids) > 0 {
		clientSecret = sids[0]
	}

	return
}
