package storeaccounting

import (
	"context"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	"github.com/surrealdb/surrealdb.go/pkg/connection/rpc"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

type storeLocalStep uint8

const (
	storeLocalSignIn storeLocalStep = iota + 1
	storeLocalNamespaceDefinition
	storeLocalNamespaceUse
	storeLocalDatabaseDefinition
	storeLocalDatabaseUse
	storeLocalReady
	storeExistingLocalSignIn
	storeExistingLocalUse
	storeExistingLocalReady
)

const storeLocalNamespaceSQL = "DEFINE NAMESPACE IF NOT EXISTS phebs;"
const storeLocalDatabaseSQL = "DEFINE DATABASE IF NOT EXISTS phebs;"

// The pinned SDK otherwise collapses a null, undefined, or absent result into
// the same nil pointer before Send can validate it. Preserve only this native
// envelope's raw result; every typed result still uses the default SDK codec.
type storeLocalReplyDecoder struct {
	*surrealcbor.Codec
}

func (decoder storeLocalReplyDecoder) Unmarshal(data []byte, value any) error {
	response, ok := value.(*connection.RPCResponse[cbor.RawMessage])
	if !ok || response == nil {
		return decoder.Codec.Unmarshal(data, value)
	}
	var envelope struct {
		ID     any                  `json:"id"`
		Error  *connection.RPCError `json:"error,omitempty"` //nolint:staticcheck // Preserve the pinned SDK RPCResponse wire error and errors.As behavior.
		Result cbor.RawMessage      `json:"result,omitempty"`
	}
	if err := decoder.Codec.Unmarshal(data, &envelope); err != nil {
		return err
	}
	*response = connection.RPCResponse[cbor.RawMessage]{ID: envelope.ID, Error: envelope.Error}
	if envelope.Result != nil {
		response.Result = &envelope.Result
	}
	return nil
}

func storeLocalAuth() surrealdb.Auth {
	return surrealdb.Auth{Username: "root", Password: "root"}
}

func storeLocalUseParams(step storeLocalStep) []any {
	if step == storeLocalNamespaceUse {
		return []any{"phebs", nil}
	}
	return []any{"phebs", "phebs"}
}

// Called under the producer mutex. Initialization is one closed sequence on
// the actual connection, not permission to invoke arbitrary control methods.
func (conn *SDKConnection) validLocalCallLocked(call *storeSDKCall, request *connection.RPCRequest) bool {
	if conn.localStep == 0 || conn.localStep == storeLocalReady || conn.localStep == storeExistingLocalReady {
		return call.local == nil && call.localStep == 0
	}
	if conn.localBusy || call.local != conn || call.localStep != conn.localStep || call.tx >= 0 {
		return false
	}
	switch call.localStep {
	case storeLocalSignIn, storeExistingLocalSignIn:
		if call.kind != 0 || call.rows != 0 || len(request.Params) != 1 {
			return false
		}
		auth, ok := request.Params[0].(surrealdb.Auth)
		return ok && auth == storeLocalAuth()
	case storeLocalNamespaceUse, storeLocalDatabaseUse, storeExistingLocalUse:
		if call.kind != 0 || call.rows != 0 || len(request.Params) != 2 || request.Params[0] != "phebs" {
			return false
		}
		if call.localStep == storeLocalNamespaceUse {
			return request.Params[1] == nil
		}
		return request.Params[1] == "phebs"
	case storeLocalNamespaceDefinition, storeLocalDatabaseDefinition:
		want := storeLocalNamespaceSQL
		if call.localStep == storeLocalDatabaseDefinition {
			want = storeLocalDatabaseSQL
		}
		return call.kind == ImplicitWrite && call.rows == 1 && call.sql == want &&
			len(call.vars) == 1 && call.vars[0] == 0xf6
	default:
		return false
	}
}

func (conn *SDKConnection) SignIn(ctx context.Context, authData any) (string, error) {
	// Use the SDK's own token decoder with this wrapper as the receiver. Calling
	// the embedded connection's SignIn would bypass our final submission gate.
	return rpc.SignIn(conn, ctx, authData)
}

func (conn *SDKConnection) Use(ctx context.Context, namespace, database string) error {
	return conn.useLocalScope(ctx, namespace, database)
}

func (conn *SDKConnection) useLocalScope(ctx context.Context, namespace string, database any) error {
	var response connection.RPCResponse[cbor.RawMessage]
	if err := connection.Send(conn, ctx, &response, "use", namespace, database); err != nil {
		return err
	}
	if response.Error != nil || response.Result == nil || !storeLocalScopeReply(*response.Result, database) {
		return ErrProtocol
	}
	return nil
}

// Native 3.2 returns the complete selected scope, not null. This closed pair
// of fixed maps accepts either key order without a general CBOR parser or map
// allocation. Missing, duplicate, extra, noncanonical or wrong-scope fields
// cannot become successful control evidence. Namespace-only uses native NONE.
func storeLocalScopeReply(raw []byte, database any) bool {
	switch string(raw) {
	case "\xa2\x69namespace\x65phebs\x68database\xc6\xf6", "\xa2\x68database\xc6\xf6\x69namespace\x65phebs":
		return database == nil
	case "\xa2\x69namespace\x65phebs\x68database\x65phebs", "\xa2\x68database\x65phebs\x69namespace\x65phebs":
		return database == "phebs"
	default:
		return false
	}
}

func storeLocalControl(ctx context.Context, db *surrealdb.DB, conn *SDKConnection, step storeLocalStep) (err error) {
	owner := conn.owner
	call, err := owner.acquire(ctx, 0, nil)
	if err != nil {
		return err
	}
	ctx, finishContext := owner.callContext(ctx)
	defer finishContext()
	defer func() {
		if recover() != nil {
			err = owner.fail(ctx, ErrProtocol)
			return
		}
		err = call.finish(ctx, err, nil)
	}()
	call.local, call.localStep = conn, step
	ctx = context.WithValue(ctx, storeCallContextKey{}, call)
	switch step {
	case storeLocalSignIn, storeExistingLocalSignIn:
		var token string
		token, err = db.SignIn(ctx, storeLocalAuth())
		if err == nil && token == "" {
			err = ErrProtocol
		}
	case storeLocalNamespaceUse:
		err = conn.useLocalScope(ctx, "phebs", nil)
	case storeLocalDatabaseUse, storeExistingLocalUse:
		err = db.Use(ctx, "phebs", "phebs")
	default:
		err = ErrDescriptor
	}
	return err
}

// InitializeLocalScope executes only the fixed five-step local sequence after
// FromConnection. No ordinary Open path supplies an accounting owner.
func InitializeLocalScope(ctx context.Context, db *surrealdb.DB, conn *SDKConnection) error {
	for step := storeLocalSignIn; step < storeLocalReady; step++ {
		var err error
		switch step {
		case storeLocalNamespaceDefinition, storeLocalDatabaseDefinition:
			sql := storeLocalNamespaceSQL
			if step == storeLocalDatabaseDefinition {
				sql = storeLocalDatabaseSQL
			}
			_, err = SDKQuery[any](ctx, conn.owner, db, sql, nil, SDKQueryRecipe{
				supported: true, rows: 1, local: conn, localStep: step,
			})
		default:
			err = storeLocalControl(ctx, db, conn, step)
		}
		if err != nil {
			return conn.owner.fail(ctx, ErrTransport)
		}
	}
	return nil
}

// InitializeExistingLocalScope preserves the existing backup connection's two
// controls. The runtime descriptor already binds an existing local database;
// this sequence adds neither metadata definitions nor store write submissions.
func InitializeExistingLocalScope(ctx context.Context, db *surrealdb.DB, conn *SDKConnection) error {
	for step := storeExistingLocalSignIn; step < storeExistingLocalReady; step++ {
		if err := storeLocalControl(ctx, db, conn, step); err != nil {
			return conn.owner.fail(ctx, ErrTransport)
		}
	}
	return nil
}

// Check validates that this concrete owner can accept new connection work.
// It is not a semantic readiness or native-connection closure claim.
func (owner *SDKOwner) Check(ctx context.Context) error {
	if owner == nil || owner.client == nil {
		return ErrConfig
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.err != nil {
		return owner.err
	}
	if ctx == nil || ctx.Err() != nil || owner.client.Context().Err() != nil {
		return context.Canceled
	}
	if owner.fenced {
		return ErrFenced
	}
	return nil
}

// NewLocalSDKConnection keeps the fixed local controls and their raw-result
// decoder on the same actual connection. The caller owns endpoint admission.
func NewLocalSDKConnection(ctx context.Context, owner *SDKOwner, config *connection.Config) (*SDKConnection, error) {
	return newLocalSDKConnection(ctx, owner, config, storeLocalSignIn)
}

// NewExistingLocalSDKConnection selects only the fixed two-control sequence.
// Its caller must admit the actual existing-local runtime endpoint and scope.
func NewExistingLocalSDKConnection(ctx context.Context, owner *SDKOwner, config *connection.Config) (*SDKConnection, error) {
	return newLocalSDKConnection(ctx, owner, config, storeExistingLocalSignIn)
}

func newLocalSDKConnection(ctx context.Context, owner *SDKOwner, config *connection.Config, first storeLocalStep) (*SDKConnection, error) {
	if err := owner.Check(ctx); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, ErrConfig
	}
	selected := *config
	selected.Unmarshaler = storeLocalReplyDecoder{Codec: surrealcbor.New()}
	native := gorillaws.New(&selected)
	conn, err := NewSDKConnection(owner, native)
	if err != nil {
		return nil, err
	}
	conn.localStep = first
	return conn, nil
}

// Connect retains the selected factory's failure attribution at the actual
// native connection boundary; it adds no retry or connection-drain claim.
func (conn *SDKConnection) Connect(ctx context.Context) error {
	if err := conn.sdkNative.Connect(ctx); err != nil {
		return conn.owner.fail(ctx, ErrTransport)
	}
	return nil
}
