package storeaccounting

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
)

type SDKQueryRecipe struct {
	read, supported bool
	rows            uint64
	local           *SDKConnection
	localStep       storeLocalStep
}

func SDKRead() SDKQueryRecipe { return SDKQueryRecipe{read: true, supported: true} }
func SDKWrite(rows uint64) SDKQueryRecipe {
	return SDKQueryRecipe{supported: true, rows: rows}
}
func SDKUnsupported() SDKQueryRecipe { return SDKQueryRecipe{} }

type storeCallContextKey struct{}

type storeNativeTransaction struct {
	used, busy bool
	native     models.UUID
	object     *surrealdb.Transaction
	connection *SDKConnection
	token      uint64
}

type storeSDKCall struct {
	owner      *SDKOwner
	connection *SDKConnection
	local      *SDKConnection
	localStep  storeLocalStep
	sql        string
	vars       cbor.RawMessage
	kind       Kind // zero is a locally tracked read
	rows       uint64
	tx         int
	consumed   bool
	replied    bool
	submission Submission
}

// SDKOwner retains every typed call and native transaction for one producer.
// One genuine producer shares this owner across all its SDK connections. The
// capacities come from the mechanically validated client, not a frozen issuer.
// No native UUID, SQL, variable bytes or outcome history leaves this process.
type SDKOwner struct {
	// ponytail: <=40 calls and 2 UUIDs, one short mutex; never held over SDK or
	// ACK I/O. Split only if measured contention warrants it.
	mu           sync.Mutex
	client       *Client
	callLimit    int
	txLimit      int
	calls        [MaximumCalls]*storeSDKCall
	transactions [MaximumTransactions]storeNativeTransaction
	fenced       bool
	err          error
}

// NewSDKOwner consumes the actual client's one-time SDK-owner claim.
func NewSDKOwner(client *Client) (*SDKOwner, error) {
	if client == nil {
		return nil, ErrConfig
	}
	calls, transactions := client.Capacities()
	if client.Context() == nil || client.Context().Err() != nil || calls < 1 || calls > MaximumCalls ||
		transactions < 1 || transactions > MaximumTransactions {
		return nil, ErrConfig
	}
	if err := client.ClaimSDKOwner(); err != nil {
		return nil, err
	}
	return &SDKOwner{client: client, callLimit: calls, txLimit: transactions}, nil
}

func (owner *SDKOwner) fail(ctx context.Context, reason error) error {
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

func (owner *SDKOwner) acquire(ctx context.Context, kind Kind, tx *surrealdb.Transaction) (*storeSDKCall, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, owner.fail(ctx, ErrCanceled)
	}
	owner.mu.Lock()
	if owner.err != nil || owner.client.Context().Err() != nil || owner.fenced {
		err, fenced := owner.err, owner.fenced
		owner.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if fenced {
			return nil, ErrFenced
		}
		return nil, owner.fail(ctx, ErrCanceled)
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
						return nil, owner.fail(ctx, ErrProtocol)
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
			if (kind == Commit || kind == Cancel) && tx.IsClosed() {
				return nil, constants.ErrTransactionClosed
			}
			return nil, owner.fail(ctx, ErrProtocol)
		}
	} else if kind == Begin {
		for i := 0; i < owner.txLimit; i++ {
			if !owner.transactions[i].used {
				txIndex = i
				break
			}
		}
	}
	if callIndex < 0 || kind == Begin && txIndex < 0 {
		owner.mu.Unlock()
		return nil, owner.fail(ctx, ErrLimit)
	}
	call := &storeSDKCall{owner: owner, kind: kind, tx: txIndex}
	owner.calls[callIndex] = call
	if txIndex >= 0 {
		if kind == Begin {
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
		if call.kind == Begin && call.tx >= 0 {
			owner.transactions[call.tx] = storeNativeTransaction{}
		}
		owner.mu.Unlock()
		return owner.fail(ctx, ErrDescriptor)
	}
	if !replied || !storeNativeReplyError(returned) ||
		(call.kind == Begin || call.kind == Commit || call.kind == Cancel) && returned != nil {
		return owner.fail(ctx, ErrTransport)
	}
	if call.kind == Begin {
		if tx == nil || tx.ID() == nil || tx.ID().IsNil() || tx.IsClosed() {
			return owner.fail(ctx, ErrProtocol)
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
			return owner.fail(ctx, ErrProtocol)
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
		if call.kind == Commit || call.kind == Cancel {
			owner.transactions[call.tx] = storeNativeTransaction{}
		} else {
			owner.transactions[call.tx].busy = false
		}
	}
	return returned
}

func (owner *SDKOwner) releaseLocked(call *storeSDKCall) {
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
func (owner *SDKOwner) callContext(ctx context.Context) (context.Context, func()) {
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
func (owner *SDKOwner) Checkpoint(ctx context.Context) error {
	owner.mu.Lock()
	if owner.err != nil {
		err := owner.err
		owner.mu.Unlock()
		return err
	}
	if owner.fenced {
		owner.mu.Unlock()
		return ErrFenced
	}
	for _, call := range owner.calls {
		if call != nil {
			owner.mu.Unlock()
			return ErrBusy
		}
	}
	for _, tx := range owner.transactions {
		if tx.used {
			owner.mu.Unlock()
			return ErrBusy
		}
	}
	owner.fenced = true
	owner.mu.Unlock()
	if err := owner.client.Checkpoint(ctx); err != nil {
		return owner.fail(ctx, err)
	}
	return nil
}

func (owner *SDKOwner) Resume(phase uint32) error {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.err != nil {
		return owner.err
	}
	if !owner.fenced {
		return ErrProtocol
	}
	if err := owner.client.Resume(phase); err != nil {
		return err
	}
	owner.fenced = false
	return nil
}

func (owner *SDKOwner) Close(ctx context.Context) error {
	owner.mu.Lock()
	if owner.err != nil {
		err := owner.err
		owner.mu.Unlock()
		return err
	}
	for _, call := range owner.calls {
		if call != nil {
			owner.mu.Unlock()
			return ErrBusy
		}
	}
	for _, tx := range owner.transactions {
		if tx.used {
			owner.mu.Unlock()
			return ErrBusy
		}
	}
	owner.fenced = true
	owner.mu.Unlock()
	if err := owner.client.Close(ctx); err != nil {
		return owner.fail(ctx, err)
	}
	return nil
}

type sdkSender interface {
	*surrealdb.DB | *surrealdb.Transaction
}

func SDKQuery[T any, S sdkSender](ctx context.Context, owner *SDKOwner, sender S, sql string, vars map[string]any, recipe SDKQueryRecipe) (result *[]surrealdb.QueryResult[T], err error) {
	if owner == nil {
		return surrealdb.Query[T](ctx, sender, sql, vars)
	}
	if !recipe.supported || recipe.rows > MaximumRows || recipe.read && recipe.rows != 0 || sql == "" {
		return nil, owner.fail(ctx, ErrDescriptor)
	}
	tx, _ := any(sender).(*surrealdb.Transaction)
	kind := Kind(0)
	if !recipe.read {
		kind = ImplicitWrite
		if tx != nil {
			if recipe.rows == 0 {
				// The already-counted Begin owns this zero-row call. There is
				// no second logical transaction or fabricated positive append.
				kind = 0
			} else {
				kind = Append
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
			result, err = nil, owner.fail(ctx, ErrProtocol)
			return
		}
		if call.local != nil && err == nil && (result == nil || len(*result) != 1 || (*result)[0].Status != "OK" || (*result)[0].Error != nil) {
			err = ErrProtocol
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

func SDKBegin(ctx context.Context, owner *SDKOwner, db *surrealdb.DB) (tx *surrealdb.Transaction, err error) {
	if owner == nil {
		return db.Begin(ctx)
	}
	call, err := owner.acquire(ctx, Begin, nil)
	if err != nil {
		return nil, err
	}
	ctx, finishContext := owner.callContext(ctx)
	defer finishContext()
	defer func() {
		if recover() != nil {
			tx, err = nil, owner.fail(ctx, ErrProtocol)
			return
		}
		err = call.finish(ctx, err, tx)
	}()
	return db.Begin(context.WithValue(ctx, storeCallContextKey{}, call))
}

func SDKCommit(ctx context.Context, owner *SDKOwner, tx *surrealdb.Transaction) error {
	return storeTerminal(ctx, owner, tx, Commit)
}
func SDKCancel(ctx context.Context, owner *SDKOwner, tx *surrealdb.Transaction) error {
	return storeTerminal(ctx, owner, tx, Cancel)
}
func storeTerminal(ctx context.Context, owner *SDKOwner, tx *surrealdb.Transaction, kind Kind) (err error) {
	if owner == nil {
		if kind == Commit {
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
			err = owner.fail(ctx, ErrProtocol)
			return
		}
		err = call.finish(ctx, err, tx)
	}()
	ctx = context.WithValue(ctx, storeCallContextKey{}, call)
	if kind == Commit {
		return tx.Commit(ctx)
	}
	return tx.Cancel(ctx)
}

// The concrete connection is installed before FromConnection. Overridden
// control methods deliberately refuse until an owning source recipe exists.
type sdkNative = connection.WebSocketConnection

type SDKConnection struct {
	sdkNative
	owner     *SDKOwner
	localStep storeLocalStep // guarded by owner.mu; zero is not a local initializer
	localBusy bool
}

// NewSDKConnection wraps an actual native SDK connection before FromConnection.
func NewSDKConnection(owner *SDKOwner, native *gorillaws.Connection) (*SDKConnection, error) {
	if owner == nil || owner.client == nil || native == nil {
		return nil, ErrConfig
	}
	return &SDKConnection{sdkNative: native, owner: owner}, nil
}

func (conn *SDKConnection) Send(ctx context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	return conn.Call(ctx, &connection.RPCRequest{Method: method, Params: params})
}

func (conn *SDKConnection) Call(ctx context.Context, request *connection.RPCRequest) (*connection.RPCResponse[cbor.RawMessage], error) {
	if ctx == nil || request == nil {
		return nil, conn.owner.fail(ctx, ErrDescriptor)
	}
	call, ok := ctx.Value(storeCallContextKey{}).(*storeSDKCall)
	if !ok || call == nil || call.owner != conn.owner {
		return nil, conn.owner.fail(ctx, ErrDescriptor)
	}
	owner := conn.owner
	owner.mu.Lock()
	valid := owner.err == nil && !owner.fenced && !call.consumed && storeAbsentUUID(request.Session) && request.ID == nil
	valid = valid && conn.validLocalCallLocked(call, request)
	transaction := uint64(0)
	var nativeTransaction models.UUID
	if call.tx >= 0 && call.kind != Begin {
		tx := owner.transactions[call.tx]
		valid = valid && tx.used && tx.busy && tx.connection == conn
		transaction = tx.token
		nativeTransaction = tx.native
		if call.kind == Commit || call.kind == Cancel {
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
	if call.kind == Begin {
		wantMethod = "begin"
	}
	if call.kind == Commit {
		wantMethod = "commit"
	}
	if call.kind == Cancel {
		wantMethod = "cancel"
	}
	switch call.localStep {
	case storeLocalSignIn, storeExistingLocalSignIn:
		wantMethod = "signin"
	case storeLocalNamespaceUse, storeLocalDatabaseUse, storeExistingLocalUse:
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
		return nil, owner.fail(ctx, ErrDescriptor)
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
		return nil, owner.fail(ctx, ErrCanceled)
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
	response, err := conn.sdkNative.Call(ctx, &forwarded)
	var rpc *connection.ServerError
	owner.mu.Lock()
	call.replied = response != nil && err == nil || errors.As(err, &rpc)
	owner.mu.Unlock()
	if response == nil && err == nil {
		return nil, owner.fail(ctx, ErrProtocol)
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

func (conn *SDKConnection) Let(ctx context.Context, _ string, _ any) error {
	return conn.owner.fail(ctx, ErrDescriptor)
}
func (conn *SDKConnection) Authenticate(ctx context.Context, _ string) error {
	return conn.owner.fail(ctx, ErrDescriptor)
}
func (conn *SDKConnection) SignUp(ctx context.Context, _ any) (string, error) {
	return "", conn.owner.fail(ctx, ErrDescriptor)
}
func (conn *SDKConnection) SignUpWithRefresh(ctx context.Context, _ any) (*connection.Tokens, error) {
	return nil, conn.owner.fail(ctx, ErrDescriptor)
}
func (conn *SDKConnection) SignInWithRefresh(ctx context.Context, _ any) (*connection.Tokens, error) {
	return nil, conn.owner.fail(ctx, ErrDescriptor)
}
func (conn *SDKConnection) Invalidate(ctx context.Context) error {
	return conn.owner.fail(ctx, ErrDescriptor)
}
func (conn *SDKConnection) Unset(ctx context.Context, _ string) error {
	return conn.owner.fail(ctx, ErrDescriptor)
}
func (conn *SDKConnection) LiveNotifications(string) (chan connection.Notification, error) {
	return nil, conn.owner.fail(conn.owner.client.Context(), ErrDescriptor)
}
func (conn *SDKConnection) CloseLiveNotifications(string) error {
	return conn.owner.fail(conn.owner.client.Context(), ErrDescriptor)
}
