package store

import (
	"context"
	"errors"
	"sync"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	"github.com/surrealdb/surrealdb.go/pkg/constants"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

type storeQueryRecipe struct {
	read, supported bool
	rows            uint64
	local           *storeAccountingConnection
	localStep       storeLocalStep
}

func storeRead() storeQueryRecipe { return storeQueryRecipe{read: true, supported: true} }
func storeWrite(rows uint64) storeQueryRecipe {
	return storeQueryRecipe{supported: true, rows: rows}
}
func storeUnsupported() storeQueryRecipe { return storeQueryRecipe{} }

type storeCallContextKey struct{}

type storeNativeTransaction struct {
	used, busy bool
	native     models.UUID
	object     *surrealdb.Transaction
	connection *storeAccountingConnection
	token      uint64
}

type storeSDKCall struct {
	owner      *storeCallOwner
	connection *storeAccountingConnection
	local      *storeAccountingConnection
	localStep  storeLocalStep
	sql        string
	vars       cbor.RawMessage
	kind       storeaccounting.Kind // zero is a locally tracked read
	rows       uint64
	tx         int
	consumed   bool
	replied    bool
	submission storeaccounting.Submission
}

// One genuine producer shares this owner across all its SDK connections. The
// capacities come from the mechanically validated client, not a frozen issuer.
// No native UUID, SQL, variable bytes or outcome history leaves this process.
type storeCallOwner struct {
	// ponytail: <=40 calls and 2 UUIDs, one short mutex; never held over SDK or
	// ACK I/O. Split only if measured contention warrants it.
	mu           sync.Mutex
	client       *storeaccounting.Client
	callLimit    int
	txLimit      int
	calls        [storeaccounting.MaximumCalls]*storeSDKCall
	transactions [storeaccounting.MaximumTransactions]storeNativeTransaction
	fenced       bool
	err          error
}

func newStoreCallOwner(client *storeaccounting.Client) (*storeCallOwner, error) {
	if client == nil {
		return nil, storeaccounting.ErrConfig
	}
	calls, transactions := client.Capacities()
	if client.Context() == nil || client.Context().Err() != nil || calls < 1 || calls > storeaccounting.MaximumCalls ||
		transactions < 1 || transactions > storeaccounting.MaximumTransactions {
		return nil, storeaccounting.ErrConfig
	}
	if err := client.ClaimSDKOwner(); err != nil {
		return nil, err
	}
	return &storeCallOwner{client: client, callLimit: calls, txLimit: transactions}, nil
}

func (owner *storeCallOwner) fail(ctx context.Context, reason error) error {
	owner.mu.Lock()
	first := owner.err == nil
	if first {
		owner.err = reason
	}
	err := owner.err
	owner.mu.Unlock()
	if first {
		_ = owner.client.Fail(ctx, reason)
	}
	return err
}

func (owner *storeCallOwner) acquire(ctx context.Context, kind storeaccounting.Kind, tx *surrealdb.Transaction) (*storeSDKCall, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, owner.fail(ctx, storeaccounting.ErrCanceled)
	}
	owner.mu.Lock()
	if owner.err != nil || owner.client.Context().Err() != nil || owner.fenced {
		err, fenced := owner.err, owner.fenced
		owner.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if fenced {
			return nil, storeaccounting.ErrFenced
		}
		return nil, owner.fail(ctx, storeaccounting.ErrCanceled)
	}
	callIndex, txIndex := -1, -1
	for i := 0; i < owner.callLimit; i++ {
		if owner.calls[i] == nil {
			callIndex = i
			break
		}
	}
	if tx != nil {
		if tx.ID() != nil {
			for i := 0; i < owner.txLimit; i++ {
				candidate := owner.transactions[i]
				if candidate.used && candidate.object == tx && candidate.native == *tx.ID() {
					if candidate.busy {
						owner.mu.Unlock()
						return nil, owner.fail(ctx, storeaccounting.ErrProtocol)
					}
					txIndex = i
					break
				}
			}
		}
		if txIndex < 0 {
			owner.mu.Unlock()
			// Only a previously closed SDK transaction can be a harmless
			// deferred terminal no-op. Never take its SDK mutex while holding
			// the owner mutex: Commit/Cancel hold it across the final gate.
			if (kind == storeaccounting.Commit || kind == storeaccounting.Cancel) && tx.IsClosed() {
				return nil, constants.ErrTransactionClosed
			}
			return nil, owner.fail(ctx, storeaccounting.ErrProtocol)
		}
	} else if kind == storeaccounting.Begin {
		for i := 0; i < owner.txLimit; i++ {
			if !owner.transactions[i].used {
				txIndex = i
				break
			}
		}
	}
	if callIndex < 0 || kind == storeaccounting.Begin && txIndex < 0 {
		owner.mu.Unlock()
		return nil, owner.fail(ctx, storeaccounting.ErrLimit)
	}
	call := &storeSDKCall{owner: owner, kind: kind, tx: txIndex}
	owner.calls[callIndex] = call
	if txIndex >= 0 {
		if kind == storeaccounting.Begin {
			owner.transactions[txIndex].used = true
		}
		owner.transactions[txIndex].busy = true
	}
	owner.mu.Unlock()
	return call, nil
}

func storeNativeReplyError(err error) bool {
	var rpc *connection.ServerError
	var query *surrealdb.QueryError
	return err == nil || errors.As(err, &rpc) || errors.As(err, &query)
}

func (call *storeSDKCall) finish(ctx context.Context, returned error, tx *surrealdb.Transaction) error {
	owner := call.owner
	owner.mu.Lock()
	consumed, replied := call.consumed, call.replied
	owner.mu.Unlock()
	if !consumed {
		// No connection submission occurred; a refused preparation adds no
		// invented attempt, but the exact producer cannot continue silently.
		owner.mu.Lock()
		owner.releaseLocked(call)
		if call.kind == storeaccounting.Begin && call.tx >= 0 {
			owner.transactions[call.tx] = storeNativeTransaction{}
		}
		owner.mu.Unlock()
		return owner.fail(ctx, storeaccounting.ErrDescriptor)
	}
	if !replied || !storeNativeReplyError(returned) ||
		(call.kind == storeaccounting.Begin || call.kind == storeaccounting.Commit || call.kind == storeaccounting.Cancel) && returned != nil {
		return owner.fail(ctx, storeaccounting.ErrTransport)
	}
	if call.kind == storeaccounting.Begin {
		if tx == nil || tx.ID() == nil || tx.ID().IsNil() || tx.IsClosed() {
			return owner.fail(ctx, storeaccounting.ErrProtocol)
		}
		owner.mu.Lock()
		duplicate := false
		for i := 0; i < owner.txLimit; i++ {
			if i != call.tx && owner.transactions[i].used && owner.transactions[i].native == *tx.ID() {
				duplicate = true
			}
		}
		if !duplicate {
			owner.transactions[call.tx].native = *tx.ID()
			owner.transactions[call.tx].object = tx
			owner.transactions[call.tx].connection = call.connection
			owner.transactions[call.tx].token = call.submission.Transaction
		}
		owner.mu.Unlock()
		if duplicate {
			return owner.fail(ctx, storeaccounting.ErrProtocol)
		}
	}
	if call.kind != 0 {
		if err := owner.client.Settle(ctx, call.submission); err != nil {
			return owner.fail(ctx, err)
		}
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.err != nil {
		return owner.err
	}
	owner.releaseLocked(call)
	if call.local != nil && returned == nil {
		call.local.localStep++
		call.local.localBusy = false
	}
	if call.tx >= 0 {
		if call.kind == storeaccounting.Commit || call.kind == storeaccounting.Cancel {
			owner.transactions[call.tx] = storeNativeTransaction{}
		} else {
			owner.transactions[call.tx].busy = false
		}
	}
	return returned
}

func (owner *storeCallOwner) releaseLocked(call *storeSDKCall) {
	for i := 0; i < owner.callLimit; i++ {
		if owner.calls[i] == call {
			owner.calls[i] = nil
			break
		}
	}
	if call.tx >= 0 {
		owner.transactions[call.tx].busy = false
	}
}

// Each selected call owns its cancellation bridge through typed decoding. A
// canceled native call still retains its uncertain reservation; cancellation is
// not proof that the engine stopped or closed a transaction.
func (owner *storeCallOwner) callContext(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	joined := make(chan struct{})
	stop := context.AfterFunc(owner.client.Context(), func() {
		cancel()
		close(joined)
	})
	return ctx, func() {
		if !stop() {
			<-joined
		}
		cancel()
	}
}

// The semantic owner must stop new work before asking for this handoff. There
// is no transaction carry and no claim that call drain proves semantic readiness.
func (owner *storeCallOwner) checkpoint(ctx context.Context) error {
	owner.mu.Lock()
	if owner.err != nil {
		err := owner.err
		owner.mu.Unlock()
		return err
	}
	if owner.fenced {
		owner.mu.Unlock()
		return storeaccounting.ErrFenced
	}
	for _, call := range owner.calls {
		if call != nil {
			owner.mu.Unlock()
			return storeaccounting.ErrBusy
		}
	}
	for _, tx := range owner.transactions {
		if tx.used {
			owner.mu.Unlock()
			return storeaccounting.ErrBusy
		}
	}
	owner.fenced = true
	owner.mu.Unlock()
	if err := owner.client.Checkpoint(ctx); err != nil {
		return owner.fail(ctx, err)
	}
	return nil
}

func (owner *storeCallOwner) resume(phase uint32) error {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.err != nil {
		return owner.err
	}
	if !owner.fenced {
		return storeaccounting.ErrProtocol
	}
	if err := owner.client.Resume(phase); err != nil {
		return err
	}
	owner.fenced = false
	return nil
}

func (owner *storeCallOwner) close(ctx context.Context) error {
	owner.mu.Lock()
	if owner.err != nil {
		err := owner.err
		owner.mu.Unlock()
		return err
	}
	for _, call := range owner.calls {
		if call != nil {
			owner.mu.Unlock()
			return storeaccounting.ErrBusy
		}
	}
	for _, tx := range owner.transactions {
		if tx.used {
			owner.mu.Unlock()
			return storeaccounting.ErrBusy
		}
	}
	owner.fenced = true
	owner.mu.Unlock()
	if err := owner.client.Close(ctx); err != nil {
		return owner.fail(ctx, err)
	}
	return nil
}

type storeSDKSender interface {
	*surrealdb.DB | *surrealdb.Transaction
}

func storeQuery[T any, S storeSDKSender](ctx context.Context, owner *storeCallOwner, sender S, sql string, vars map[string]any, recipe storeQueryRecipe) (result *[]surrealdb.QueryResult[T], err error) {
	if owner == nil {
		return surrealdb.Query[T](ctx, sender, sql, vars)
	}
	if !recipe.supported || recipe.rows > storeaccounting.MaximumRows || recipe.read && recipe.rows != 0 || sql == "" {
		return nil, owner.fail(ctx, storeaccounting.ErrDescriptor)
	}
	tx, _ := any(sender).(*surrealdb.Transaction)
	kind := storeaccounting.Kind(0)
	if !recipe.read {
		kind = storeaccounting.ImplicitWrite
		if tx != nil {
			if recipe.rows == 0 {
				// The already-counted Begin owns this zero-row call. There is
				// no second logical transaction or fabricated positive append.
				kind = 0
			} else {
				kind = storeaccounting.Append
			}
		}
	}
	call, err := owner.acquire(ctx, kind, tx)
	if err != nil {
		return nil, err
	}
	ctx, finishContext := owner.callContext(ctx)
	defer finishContext()
	defer func() {
		if recover() != nil {
			result, err = nil, owner.fail(ctx, storeaccounting.ErrProtocol)
			return
		}
		if call.local != nil && err == nil && (result == nil || len(*result) != 1 || (*result)[0].Status != "OK" || (*result)[0].Error != nil) {
			err = storeaccounting.ErrProtocol
		}
		err = call.finish(ctx, err, nil)
	}()
	call.sql, call.rows = sql, recipe.rows
	call.local, call.localStep = recipe.local, recipe.localStep
	// Selected mode owns one immutable variable snapshot until typed decoding
	// finishes. This is extra serialization/live-buffer cost, not a byte cap.
	call.vars, err = surrealcbor.New().Marshal(vars)
	if err != nil {
		return nil, err
	}
	return surrealdb.Query[T](context.WithValue(ctx, storeCallContextKey{}, call), sender, sql, nil)
}

func storeBegin(ctx context.Context, owner *storeCallOwner, db *surrealdb.DB) (tx *surrealdb.Transaction, err error) {
	if owner == nil {
		return db.Begin(ctx)
	}
	call, err := owner.acquire(ctx, storeaccounting.Begin, nil)
	if err != nil {
		return nil, err
	}
	ctx, finishContext := owner.callContext(ctx)
	defer finishContext()
	defer func() {
		if recover() != nil {
			tx, err = nil, owner.fail(ctx, storeaccounting.ErrProtocol)
			return
		}
		err = call.finish(ctx, err, tx)
	}()
	return db.Begin(context.WithValue(ctx, storeCallContextKey{}, call))
}

func storeCommit(ctx context.Context, owner *storeCallOwner, tx *surrealdb.Transaction) error {
	return storeTerminal(ctx, owner, tx, storeaccounting.Commit)
}
func storeCancel(ctx context.Context, owner *storeCallOwner, tx *surrealdb.Transaction) error {
	return storeTerminal(ctx, owner, tx, storeaccounting.Cancel)
}
func storeTerminal(ctx context.Context, owner *storeCallOwner, tx *surrealdb.Transaction, kind storeaccounting.Kind) (err error) {
	if owner == nil {
		if kind == storeaccounting.Commit {
			return tx.Commit(ctx)
		}
		return tx.Cancel(ctx)
	}
	call, err := owner.acquire(ctx, kind, tx)
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
		err = call.finish(ctx, err, tx)
	}()
	ctx = context.WithValue(ctx, storeCallContextKey{}, call)
	if kind == storeaccounting.Commit {
		return tx.Commit(ctx)
	}
	return tx.Cancel(ctx)
}

// The concrete connection is installed before FromConnection. Overridden
// control methods deliberately refuse until an owning source recipe exists.
type storeAccountingConnection struct {
	connection.WebSocketConnection
	owner     *storeCallOwner
	localStep storeLocalStep // guarded by owner.mu; zero is not a local initializer
	localBusy bool
}

func newStoreAccountingConnection(owner *storeCallOwner, native *gorillaws.Connection) (*storeAccountingConnection, error) {
	if owner == nil || owner.client == nil || native == nil {
		return nil, storeaccounting.ErrConfig
	}
	return &storeAccountingConnection{WebSocketConnection: native, owner: owner}, nil
}

func (conn *storeAccountingConnection) Send(ctx context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	return conn.Call(ctx, &connection.RPCRequest{Method: method, Params: params})
}

func (conn *storeAccountingConnection) Call(ctx context.Context, request *connection.RPCRequest) (*connection.RPCResponse[cbor.RawMessage], error) {
	if ctx == nil || request == nil {
		return nil, conn.owner.fail(ctx, storeaccounting.ErrDescriptor)
	}
	call, ok := ctx.Value(storeCallContextKey{}).(*storeSDKCall)
	if !ok || call == nil || call.owner != conn.owner {
		return nil, conn.owner.fail(ctx, storeaccounting.ErrDescriptor)
	}
	owner := conn.owner
	owner.mu.Lock()
	valid := owner.err == nil && !owner.fenced && !call.consumed && storeAbsentUUID(request.Session) && request.ID == nil
	valid = valid && conn.validLocalCallLocked(call, request)
	transaction := uint64(0)
	var nativeTransaction models.UUID
	if call.tx >= 0 && call.kind != storeaccounting.Begin {
		tx := owner.transactions[call.tx]
		valid = valid && tx.used && tx.busy && tx.connection == conn
		transaction = tx.token
		nativeTransaction = tx.native
		if call.kind == storeaccounting.Commit || call.kind == storeaccounting.Cancel {
			id, good := firstStoreUUID(request.Params)
			valid = valid && good && id == tx.native && storeAbsentUUID(request.Txn)
		} else {
			id, good := request.Txn.(*models.UUID)
			valid = valid && good && id != nil && *id == tx.native
		}
	} else {
		valid = valid && storeAbsentUUID(request.Txn)
	}
	wantMethod := "query"
	if call.kind == storeaccounting.Begin {
		wantMethod = "begin"
	}
	if call.kind == storeaccounting.Commit {
		wantMethod = "commit"
	}
	if call.kind == storeaccounting.Cancel {
		wantMethod = "cancel"
	}
	switch call.localStep {
	case storeLocalSignIn:
		wantMethod = "signin"
	case storeLocalNamespaceUse, storeLocalDatabaseUse:
		wantMethod = "use"
	}
	valid = valid && request.Method == wantMethod
	switch wantMethod {
	case "query":
		if len(request.Params) != 2 {
			valid = false
		} else {
			sql, ok := request.Params[0].(string)
			placeholder, varsOK := request.Params[1].(map[string]any)
			valid = valid && ok && sql == call.sql && varsOK && placeholder == nil && call.vars != nil
		}
	case "begin":
		valid = valid && len(request.Params) == 0
	}
	if !valid {
		owner.mu.Unlock()
		return nil, owner.fail(ctx, storeaccounting.ErrDescriptor)
	}
	call.consumed, call.connection = true, conn
	if call.local != nil {
		conn.localBusy = true
	}
	owner.mu.Unlock()
	if call.kind != 0 {
		submission, err := owner.client.Submit(ctx, call.kind, transaction, call.rows)
		if err != nil {
			return nil, owner.fail(ctx, err)
		}
		call.submission = submission
	}
	owner.mu.Lock()
	failed := owner.err
	owner.mu.Unlock()
	if failed != nil {
		return nil, failed
	}
	if ctx.Err() != nil || owner.client.Context().Err() != nil {
		return nil, owner.fail(ctx, storeaccounting.ErrCanceled)
	}
	forwarded := *request
	switch wantMethod {
	case "query":
		forwarded.Params = []any{call.sql, call.vars}
		if call.tx >= 0 {
			forwarded.Txn = &nativeTransaction
		}
	case "commit", "cancel":
		// SDK ID() exposes a mutable pointer. Forward only the validated slot
		// copy, never that externally mutable pointer after the final gate.
		forwarded.Params = []any{&nativeTransaction}
	case "signin":
		forwarded.Params = []any{storeLocalAuth()}
	case "use":
		forwarded.Params = storeLocalUseParams(call.localStep)
	}
	response, err := conn.WebSocketConnection.Call(ctx, &forwarded)
	var rpc *connection.ServerError
	owner.mu.Lock()
	call.replied = response != nil && err == nil || errors.As(err, &rpc)
	owner.mu.Unlock()
	if response == nil && err == nil {
		return nil, owner.fail(ctx, storeaccounting.ErrProtocol)
	}
	return response, err
}

func firstStoreUUID(params []any) (models.UUID, bool) {
	if len(params) != 1 {
		return models.UUID{}, false
	}
	id, ok := params[0].(*models.UUID)
	if !ok || id == nil {
		return models.UUID{}, false
	}
	return *id, true
}

func storeAbsentUUID(value any) bool {
	if value == nil {
		return true
	}
	id, ok := value.(*models.UUID)
	return ok && id == nil
}

func (conn *storeAccountingConnection) Let(ctx context.Context, _ string, _ any) error {
	return conn.owner.fail(ctx, storeaccounting.ErrDescriptor)
}
func (conn *storeAccountingConnection) Authenticate(ctx context.Context, _ string) error {
	return conn.owner.fail(ctx, storeaccounting.ErrDescriptor)
}
func (conn *storeAccountingConnection) SignUp(ctx context.Context, _ any) (string, error) {
	return "", conn.owner.fail(ctx, storeaccounting.ErrDescriptor)
}
func (conn *storeAccountingConnection) SignUpWithRefresh(ctx context.Context, _ any) (*connection.Tokens, error) {
	return nil, conn.owner.fail(ctx, storeaccounting.ErrDescriptor)
}
func (conn *storeAccountingConnection) SignInWithRefresh(ctx context.Context, _ any) (*connection.Tokens, error) {
	return nil, conn.owner.fail(ctx, storeaccounting.ErrDescriptor)
}
func (conn *storeAccountingConnection) Invalidate(ctx context.Context) error {
	return conn.owner.fail(ctx, storeaccounting.ErrDescriptor)
}
func (conn *storeAccountingConnection) Unset(ctx context.Context, _ string) error {
	return conn.owner.fail(ctx, storeaccounting.ErrDescriptor)
}
func (conn *storeAccountingConnection) LiveNotifications(string) (chan connection.Notification, error) {
	return nil, conn.owner.fail(conn.owner.client.Context(), storeaccounting.ErrDescriptor)
}
func (conn *storeAccountingConnection) CloseLiveNotifications(string) error {
	return conn.owner.fail(conn.owner.client.Context(), storeaccounting.ErrDescriptor)
}
