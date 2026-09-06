package store

import (
	"context"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/rpc"
	"github.com/surrealdb/surrealdb.go/surrealcbor"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

type storeLocalStep uint8

const (
	storeLocalSignIn storeLocalStep = iota + 1
	storeLocalNamespaceDefinition
	storeLocalNamespaceUse
	storeLocalDatabaseDefinition
	storeLocalDatabaseUse
	storeLocalReady
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
func (conn *storeAccountingConnection) validLocalCallLocked(call *storeSDKCall, request *connection.RPCRequest) bool {
	if conn.localStep == 0 || conn.localStep == storeLocalReady {
		return call.local == nil && call.localStep == 0
	}
	if conn.localBusy || call.local != conn || call.localStep != conn.localStep || call.tx >= 0 {
		return false
	}
	switch call.localStep {
	case storeLocalSignIn:
		if call.kind != 0 || call.rows != 0 || len(request.Params) != 1 {
			return false
		}
		auth, ok := request.Params[0].(surrealdb.Auth)
		return ok && auth == storeLocalAuth()
	case storeLocalNamespaceUse, storeLocalDatabaseUse:
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
		return call.kind == storeaccounting.ImplicitWrite && call.rows == 1 && call.sql == want &&
			len(call.vars) == 1 && call.vars[0] == 0xf6
	default:
		return false
	}
}

func (conn *storeAccountingConnection) SignIn(ctx context.Context, authData any) (string, error) {
	// Use the SDK's own token decoder with this wrapper as the receiver. Calling
	// the embedded connection's SignIn would bypass our final submission gate.
	return rpc.SignIn(conn, ctx, authData)
}

func (conn *storeAccountingConnection) Use(ctx context.Context, namespace, database string) error {
	return conn.useLocalScope(ctx, namespace, database)
}

func (conn *storeAccountingConnection) useLocalScope(ctx context.Context, namespace string, database any) error {
	var response connection.RPCResponse[cbor.RawMessage]
	if err := connection.Send(conn, ctx, &response, "use", namespace, database); err != nil {
		return err
	}
	if response.Error != nil || response.Result == nil || len(*response.Result) != 1 || (*response.Result)[0] != 0xf6 {
		return storeaccounting.ErrProtocol
	}
	return nil
}

func storeLocalControl(ctx context.Context, db *surrealdb.DB, conn *storeAccountingConnection, step storeLocalStep) (err error) {
	owner := conn.owner
	call, err := owner.acquire(ctx, 0, nil)
	if err != nil {
		return err
	}
	ctx, finishContext := owner.callContext(ctx)
	defer finishContext()
	defer func() {
		if recover() != nil {
			err = owner.fail(ctx, storeaccounting.ErrProtocol)
			return
		}
		err = call.finish(ctx, err, nil)
	}()
	call.local, call.localStep = conn, step
	ctx = context.WithValue(ctx, storeCallContextKey{}, call)
	switch step {
	case storeLocalSignIn:
		var token string
		token, err = db.SignIn(ctx, storeLocalAuth())
		if err == nil && token == "" {
			err = storeaccounting.ErrProtocol
		}
	case storeLocalNamespaceUse:
		err = conn.useLocalScope(ctx, "phebs", nil)
	case storeLocalDatabaseUse:
		err = db.Use(ctx, "phebs", "phebs")
	default:
		err = storeaccounting.ErrDescriptor
	}
	return err
}

// Only the private selected factory calls this after FromConnection. Until the
// genuine producer bootstrap exists, no ordinary Open path supplies an owner.
func initializeAccountedLocalScope(ctx context.Context, db *surrealdb.DB, conn *storeAccountingConnection) error {
	for step := storeLocalSignIn; step < storeLocalReady; step++ {
		var err error
		switch step {
		case storeLocalNamespaceDefinition, storeLocalDatabaseDefinition:
			sql := storeLocalNamespaceSQL
			if step == storeLocalDatabaseDefinition {
				sql = storeLocalDatabaseSQL
			}
			_, err = storeQuery[any](ctx, conn.owner, db, sql, nil, storeQueryRecipe{
				supported: true, rows: 1, local: conn, localStep: step,
			})
		default:
			err = storeLocalControl(ctx, db, conn, step)
		}
		if err != nil {
			return conn.owner.fail(ctx, storeaccounting.ErrTransport)
		}
	}
	return nil
}
