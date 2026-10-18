"""Source-only authoring helper: explicit human choices, literal spans, no Go parser.
Do not run a candidate or derive labels from emitted facts here.
"""
from pathlib import Path
import hashlib
import json

ROOT = Path(__file__).resolve().parent
PUBLIC = ROOT / 'inputs/public'

def digest(b):
    return hashlib.sha256(b).hexdigest()

def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False) + '\n')

def span(path, text, line=None, occurrence=1):
    b = path.read_bytes()
    needle = text.encode()
    if line is not None:
        lines = b.splitlines(keepends=True)
        prefix = sum(map(len, lines[:line-1]))
        region = lines[line-1]
    else:
        prefix, region = 0, b
    pos = -1
    for _ in range(occurrence):
        pos = region.find(needle, pos+1)
        if pos < 0:
            raise ValueError((str(path), text, line, occurrence))
    start = prefix + pos
    return {'path': str(path.relative_to(ROOT)), 'start_byte': start,
            'end_byte': start+len(needle), 'start_line': b[:start].count(b'\n')+1,
            'end_line': b[:start+len(needle)].count(b'\n')+1,
            'text': text, 'sha256': digest(needle)}

# Every entry below was explicitly chosen after reading the three full files.
# No lexer/parser/regex discovers or classifies any call expression.
public_callees = {
 'transport_udp.go': [
 (39,'errors.New'),(71,'len'),(72,'fmt.Sprintf'),(83,'thrift.NewTCompactProtocolFactory'),
 (86,'thrift.NewTMemoryBufferLen'),(87,'protocolFactory.GetProtocol'),
 (89,'utils.NewAgentClientUDPWithParams'),(105,'NewUDPTransportWithParams'),
 (119,'s.thriftBuffer.Reset'),(120,'thriftStruct.Write'),(120,'context.Background'),
 (121,'s.thriftBuffer.Len'),(126,'BuildJaegerProcessThrift'),
 (127,'s.calcSizeOfSerializedThrift'),(130,'BuildJaegerThrift'),
 (131,'s.calcSizeOfSerializedThrift'),(139,'append'),(143,'s.Flush'),(146,'s.Flush'),
 (147,'append'),(153,'len'),(158,'int64'),(159,'s.client.EmitBatch'),
 (159,'context.Background'),(163,'s.makeStats'),(165,'s.resetBuffers'),
 (167,'int64'),(173,'s.client.Close'),(187,'s.reporterStats.SpansDroppedFromQueue')],
 'utils/udp_client.go': [
 (66,'net.SplitHostPort'),(82,'thrift.NewTMemoryBufferLen'),
 (83,'thrift.NewTCompactProtocolFactory'),(84,'agent.NewAgentClientFactory'),
 (90,'net.ResolveUDPAddr'),(95,'net.DialUDP'),(95,'destAddr.Network'),
 (101,'newReconnectingUDPConn'),(107,'connUDP.SetWriteBuffer'),
 (121,'NewAgentClientUDPWithParams'),(129,'errors.New'),(134,'a.thriftBuffer.Reset'),
 (135,'a.client.EmitBatch'),(138,'a.thriftBuffer.Len'),(139,'fmt.Errorf'),
 (140,'a.thriftBuffer.Len'),(140,'len'),(142,'a.connUDP.Write'),
 (142,'a.thriftBuffer.Bytes'),(148,'a.connUDP.Close')],
 'testutils/mock_agent.go': [
 (38,'NewTUDPServerTransport'),(43,'newSamplingManager'),(45,'httptest.NewServer'),
 (54,'started.Add'),(55,'agent.serve'),(56,'started.Wait'),(63,'atomic.StoreUint32'),
 (64,'s.transport.Close'),(65,'s.samplingSrv.Close'),(81,'s.transport.Addr'),
 (81,'s.transport.Addr().String'),(86,'utils.NewAgentClientUDP'),(86,'s.SpanServerAddr'),
 (91,'s.samplingSrv.Listener.Addr'),(91,'s.samplingSrv.Listener.Addr().String'),
 (95,'agent.NewAgentProcessor'),(96,'thrift.NewTCompactProtocolFactory'),(97,'make'),
 (98,'thrift.NewTMemoryBufferLen'),(100,'atomic.StoreUint32'),(101,'started.Done'),
 (102,'s.IsServing'),(103,'s.transport.Read'),(105,'trans.Write'),
 (106,'protocolFact.GetProtocol'),(107,'handler.Process'),(107,'context.Background'),
 (115,'errors.New'),(128,'s.mutex.Lock'),(129,'s.mutex.Unlock'),(130,'append'),
 (136,'atomic.LoadUint32'),(141,'s.samplingMgr.AddSamplingStrategy'),
 (146,'s.mutex.Lock'),(147,'s.mutex.Unlock'),(148,'len'),(149,'make'),(150,'copy'),
 (156,'s.mutex.Lock'),(157,'s.mutex.Unlock'),(166,'r.URL.Query'),(167,'len'),
 (168,'http.Error'),(171,'len'),(172,'http.Error'),(175,'h.manager.GetSamplingStrategy'),
 (177,'http.Error'),(177,'fmt.Sprintf'),(180,'json.Marshal'),(182,'http.Error'),
 (185,'w.Header'),(185,'w.Header().Add'),(186,'w.Write')]
}
public_rows=[]
for file, calls in public_callees.items():
    for line, callee in calls:
        role='excluded-other-call'
        expected={}
        if file=='testutils/mock_agent.go' and line==175:
            role='unproved-generated-method-name'
            expected={'positive_claims':0,'state':'unproved receiver: local *samplingManager has no generated-client/delegation witness in the selected input'}
        if (file,line) in [('transport_udp.go',159),('utils/udp_client.go',135)] and callee.endswith('.EmitBatch'):
            role='supported-method-call'
            expected={'predicate':'CALLS_OPERATION','tier':'heuristic','scope_hint':'agent',
                      'service_hint':'Agent','method_hint':'EmitBatch','wire_hint':'emitBatch'}
        if file=='testutils/mock_agent.go' and line==95:
            role='supported-processor-construction'
            expected={'predicate':'REGISTERS_THRIFT_SERVICE','tier':'derived',
                      'scope_hint':'agent','service_hint':'Agent','constructor_hint':'NewAgentProcessor'}
        public_rows.append({'id':f'public-{len(public_rows)+1:03}', 'role':role,
                            'callee':span(PUBLIC/file,callee,line), 'expected':expected})
write_json(ROOT/'public-sites.json', {'schema':'t582-explicit-source-call-census-v1',
    'authoring':'Complete handwritten-file reading then explicit line/callee literal choices; no parser/candidate output.',
    'sites':public_rows})

# Generated service facts selected by hand from interface/client/processor sections.
services=[('agent','Agent',[('EmitZipkinBatch','emitZipkinBatch'),('EmitBatch','emitBatch')]),
          ('baggage','BaggageRestrictionManager',[('GetBaggageRestrictions','getBaggageRestrictions')]),
          ('jaeger','Collector',[('SubmitBatches','submitBatches')]),
          ('sampling','SamplingManager',[('GetSamplingStrategy','getSamplingStrategy')]),
          ('zipkincore','ZipkinCollector',[('SubmitZipkinBatch','submitZipkinBatch')])]
witnesses=[]
for package,service,methods in services:
    p=PUBLIC/f'thrift-gen/{package}/{package}.go'
    witnesses.append({'scope_hint':package,'service_hint':service,
      'header':span(p,'// Code generated by Thrift Compiler (0.14.1). DO NOT EDIT.',1),
      'package':span(p,f'package {package}'),
      'interface':span(p,f'type {service} interface {{'),
      'client_type':span(p,f'type {service}Client struct {{'),
      'constructor':span(p,f'func New{service}Processor(handler {service}) *{service}Processor {{'),
      'methods':[{'go_name':go,'wire_literal':wire,
        'interface_method':span(p,go+'(ctx context.Context'),
        'client_method':span(p,f'func (p *{service}Client) {go}(ctx context.Context'),
        'call_literal':span(p,f'Call(ctx, "{wire}"'),
        'processor_map_literal':span(p,f'processorMap["{wire}"]')}
        for go,wire in methods]})
write_json(ROOT/'generated-witnesses.json',{'schema':'t582-generated-source-hints-v1',
 'identity_limit':'Source text correspondence only; no exact IDL namespace, receiver or runtime identity.',
 'services':witnesses})

# Independent public positive source traces. These are explicit source literals,
# not an alias resolver or a type-checker result.
traces=[
 {'site':'public-022','why':'udpSender.client is the selected local UDP wrapper; its EmitBatch body delegates to a generated AgentClient.',
  'witnesses':[span(PUBLIC/'transport_udp.go','client          *utils.AgentClientUDP'),
   span(PUBLIC/'utils/udp_client.go','func (a *AgentClientUDP) EmitBatch(ctx context.Context, batch *jaeger.Batch) error {'),
   span(PUBLIC/'utils/udp_client.go','client        *agent.AgentClient'),
   span(PUBLIC/'utils/udp_client.go','agent.NewAgentClientFactory(thriftBuffer, protocolFactory)'),
   span(PUBLIC/'utils/udp_client.go','a.client.EmitBatch(ctx, batch)')]},
 {'site':'public-041','why':'The client field is explicitly generated AgentClient-typed and initialized by its generated client factory.',
  'witnesses':[span(PUBLIC/'utils/udp_client.go','client        *agent.AgentClient'),
   span(PUBLIC/'utils/udp_client.go','agent.NewAgentClientFactory(thriftBuffer, protocolFactory)'),
   span(PUBLIC/'utils/udp_client.go','"github.com/uber/jaeger-client-go/thrift-gen/agent"')]},
 {'site':'public-064','why':'Unshadowed generated import in MockAgent.serve calls the generated processor constructor; construction only.',
  'witnesses':[span(PUBLIC/'testutils/mock_agent.go','"github.com/uber/jaeger-client-go/thrift-gen/agent"'),
   span(PUBLIC/'testutils/mock_agent.go','func (s *MockAgent) serve(started *sync.WaitGroup) {'),
   span(PUBLIC/'testutils/mock_agent.go','handler := agent.NewAgentProcessor(s)')]}
]
# Bind IDs by exact chosen site rather than relying on the displayed ordinal.
for tr, file, line in zip(traces,['transport_udp.go','utils/udp_client.go','testutils/mock_agent.go'],[159,135,95]):
    matching=[x for x in public_rows if x['callee']['path']==f'inputs/public/{file}' and x['callee']['start_line']==line and x['role'].startswith('supported-')]
    assert len(matching)==1
    tr['site']=matching[0]['id']
write_json(ROOT/'public-positive-traces.json',{'schema':'t582-source-supported-candidates-v1','traces':traces})

controls_root=ROOT/'inputs/controls'; controls_root.mkdir(parents=True,exist_ok=True)
header='// Authored Apache-2.0 T58.2 source-only control; never execute.\n'
imports='import ("context"; agent "github.com/uber/jaeger-client-go/thrift-gen/agent")\n'
base='package probe\n'+imports
cases=[]
def add(id,kind,code,sites,context='public',required=None):
    p=controls_root/(id+'.go');p.write_text(header+code)
    rows=[]
    for text, role, expected in sites:
        rows.append({'site':span(p,text),'role':role,'expected':expected})
    cases.append({'id':id,'kind':kind,'source':str(p.relative_to(ROOT)),
                  'context':context,'sites':rows,'required_file_outcome':required})
call={'predicate':'CALLS_OPERATION','tier':'heuristic','scope_hint':'agent','service_hint':'Agent','method_hint':'EmitBatch','wire_hint':'emitBatch'}
reg={'predicate':'REGISTERS_THRIFT_SERVICE','tier':'derived','scope_hint':'agent','service_hint':'Agent','constructor_hint':'NewAgentProcessor'}
add('c01-direct-client','supported-call',base+'func probe(c *agent.AgentClient, ctx context.Context) { _ = c.EmitBatch(ctx,nil) }\n',[('c.EmitBatch','supported-method-call',call)])
add('c02-go-defer','supported-call',base+'func probe(c *agent.AgentClient, ctx context.Context) {\n go c.EmitBatch(ctx,nil)\n defer c.EmitBatch(ctx,nil)\n}\n',[('go c.EmitBatch','supported-method-call',call),('defer c.EmitBatch','supported-method-call',call)])
for i,row in enumerate(cases[-1]['sites'],1):
    row['site']=span(controls_root/'c02-go-defer.go','c.EmitBatch',occurrence=i)
add('c03-constructor','supported-construction',base+'func probe(h agent.Agent) { _ = agent.NewAgentProcessor(h) }\n',[('agent.NewAgentProcessor','supported-processor-construction',reg)])
add('c04-dot-constructor','supported-construction','package probe\nimport . "github.com/uber/jaeger-client-go/thrift-gen/agent"\nfunc probe(h Agent) { _ = NewAgentProcessor(h) }\n',[('NewAgentProcessor','supported-processor-construction',reg)])
add('c05-foreign-receiver','semantic-lookalike','package probe\nimport "context"\ntype ordinary struct{}\nfunc (*ordinary) EmitBatch(context.Context,any) error { return nil }\nfunc probe(o *ordinary, ctx context.Context) { _ = o.EmitBatch(ctx,nil) }\n',[('o.EmitBatch','lookalike-no-positive',{})])
add('c06-local-constructor','semantic-lookalike','package probe\nfunc NewAgentProcessor(any) any { return nil }\nfunc probe() { _ = NewAgentProcessor(nil) }\n',[('NewAgentProcessor(nil)','lookalike-no-positive',{})])
add('c07-foreign-constructor','semantic-lookalike','package probe\nimport alien "example.invalid/unavailable/agent"\nfunc probe() { _ = alien.NewAgentProcessor(nil) }\n',[('alien.NewAgentProcessor','lookalike-no-positive',{})])
add('c08-shadowed-alias','semantic-lookalike','package probe\nimport agent "github.com/uber/jaeger-client-go/thrift-gen/agent"\nvar _ agent.Agent\ntype localFactory struct{}\nfunc (localFactory) NewAgentProcessor(any) any { return nil }\nfunc probe() {\n agent := localFactory{}\n _ = agent.NewAgentProcessor(nil)\n}\n',[('agent.NewAgentProcessor(nil)','lookalike-no-positive',{})])
add('c09-method-value','dynamic',base+'func probe(c *agent.AgentClient, ctx context.Context) {\n call := c.EmitBatch\n _ = call(ctx,nil)\n}\n',[('call(ctx,nil)','dynamic-no-positive',{})])
add('c10-constructor-value','dynamic',base+'func probe(h agent.Agent) {\n ctor := agent.NewAgentProcessor\n _ = ctor(h)\n}\n',[('ctor(h)','dynamic-no-positive',{})])
add('c11-indexed-callback','dynamic',base+'func probe(c *agent.AgentClient, ctx context.Context, index int) {\n callbacks := []func(context.Context,*jaeger.Batch) error{c.EmitBatch}\n _ = callbacks[index](ctx,nil)\n}\n',[('callbacks[index](ctx,nil)','dynamic-no-positive',{})])
# Include the jaeger type import in this inert, syntactically valid source.
p=controls_root/'c11-indexed-callback.go';p.write_text(p.read_text().replace('"context";', '"context"; "github.com/uber/jaeger-client-go/thrift-gen/jaeger";'))
cases[-1]['sites'][0]['site']=span(p,'callbacks[index](ctx,nil)')
add('c12-wrapper','wrapper',base+'func wrap(h agent.Agent) any { return agent.NewAgentProcessor(h) }\nfunc probe(h agent.Agent) { _ = wrap(h) }\n',[('agent.NewAgentProcessor','supported-processor-construction',reg),('wrap(h)','wrapper-outer-no-positive',{})])
add('c13-client-construction','excluded',base+'func probe() { _ = agent.NewAgentClientFactory(nil,nil) }\n',[('agent.NewAgentClientFactory','client-construction-no-positive',{})])
add('c14-method-ambiguity','ambiguity',base+'func probe(c *agent.AgentClient,ctx context.Context) { _ = c.EmitBatch(ctx,nil) }\n',[('c.EmitBatch','ambiguous-method-call',{'predicate':'UNRESOLVED_THRIFT_CALL','candidate_count':2,'candidates':[{'scope_hint':'agent','service_hint':'Agent','wire_hint':'emitBatch'},{'scope_hint':'otheragent','service_hint':'Agent','wire_hint':'emitBatch'}]})],context='duplicate-agent')
add('c15-constructor-ambiguity','ambiguity',base+'func probe(h agent.Agent) { _ = agent.NewAgentProcessor(h) }\n',[('agent.NewAgentProcessor','ambiguous-processor-construction',{'predicate':'UNRESOLVED_THRIFT_REGISTRATION','candidate_count':2,'candidates':[{'scope_hint':'agent','service_hint':'Agent'},{'scope_hint':'otheragent','service_hint':'Agent'}]})],context='duplicate-agent')
add('c16-no-context','unsupported-context',base+'func probe(c *agent.AgentClient,ctx context.Context) { _ = c.EmitBatch(ctx,nil) }\n',[('c.EmitBatch','unsupported-no-positive',{})],context='none')
add('c17-unsupported-generator','unsupported-context',base+'func probe(c *agent.AgentClient,ctx context.Context) { _ = c.EmitBatch(ctx,nil) }\n',[('c.EmitBatch','unsupported-no-positive',{})],context='unsupported-agent')
add('c18-header-string','unsupported-context',base+'func probe(c *agent.AgentClient,ctx context.Context) { _ = c.EmitBatch(ctx,nil) }\n',[('c.EmitBatch','unsupported-no-positive',{})],context='header-string-agent')
add('c19-legacy-header','synthetic-version',base+'func probe(c *agent.AgentClient,ctx context.Context) { _ = c.EmitBatch(ctx,nil) }\n',[('c.EmitBatch','supported-method-call',call)],context='legacy-agent')
add('c20-modern-019-header','synthetic-version',base+'func probe(c *agent.AgentClient,ctx context.Context) { _ = c.EmitBatch(ctx,nil) }\n',[('c.EmitBatch','supported-method-call',call)],context='modern-019-agent')
add('c21-malformed','file-gap','package probe\nfunc broken( {\n',[],required={'predicate':'THRIFT_EXTRACTION_GAP','reason':'parse_error','positive_claims':0})
add('c22-empty','empty','',[],context='public',required={'gap_claims':0,'positive_claims':0})
# A truly empty source is required, without the ordinary authored-control header.
(controls_root/'c22-empty.go').write_bytes(b'')
add('c23-generated-only','generated-only','',[],required={'positive_claims':0,'gap_claims':0})
(controls_root/'c23-generated-only.go').unlink()
cases[-1]['source']=None
add('c24-comments-strings','excluded','package probe\n// c.EmitBatch(ctx,nil); agent.NewAgentProcessor(h)\nconst text = "c.EmitBatch(ctx,nil); agent.NewAgentProcessor(h)"\n',[],required={'positive_claims':0,'gap_claims':0})
add('c25-handler-call','semantic-lookalike',base+'type handler struct{}\nfunc (*handler) EmitBatch(context.Context,*jaeger.Batch) error { return nil }\nfunc probe(h *handler,ctx context.Context) { _ = h.EmitBatch(ctx,nil) }\n',[('h.EmitBatch','lookalike-no-positive',{})])
p=controls_root/'c25-handler-call.go';p.write_text(p.read_text().replace('"context";', '"context"; "github.com/uber/jaeger-client-go/thrift-gen/jaeger";'))
cases[-1]['sites'][0]['site']=span(p,'h.EmitBatch')

ctxroot=ROOT/'inputs/authored-context';ctxroot.mkdir(parents=True,exist_ok=True)
agent=(PUBLIC/'thrift-gen/agent/agent.go').read_bytes()
variants={
 'duplicate-agent':agent.replace(b'package agent\n',b'package otheragent\n',1),
 'unsupported-agent':agent.replace(b'// Code generated by Thrift Compiler (0.14.1). DO NOT EDIT.',b'// Code generated by thriftgo (0.4.2). DO NOT EDIT.',1),
 'header-string-agent':agent.split(b'\n',1)[1].replace(b'var _ = thrift.ZERO', b'const FakeHeader = "Code generated by Thrift Compiler (0.14.1). DO NOT EDIT."\nvar _ = thrift.ZERO',1),
 'legacy-agent':agent.replace(b'// Code generated by Thrift Compiler (0.14.1). DO NOT EDIT.',b'// Autogenerated by Thrift Compiler (0.14.1)',1),
 'modern-019-agent':agent.replace(b'// Code generated by Thrift Compiler (0.14.1). DO NOT EDIT.',b'// Code generated by Thrift Compiler (0.19.0). DO NOT EDIT.',1)}
modifications={
 'duplicate-agent':'renamed package agent to otheragent for ambiguity control',
 'unsupported-agent':'replaced generator header with unsupported thriftgo marker',
 'header-string-agent':'removed real header and inserted a header-like Go string constant',
 'legacy-agent':'replaced modern generator header with authored legacy header',
 'modern-019-agent':'changed generator version header from 0.14.1 to 0.19.0; no generator run'
}
for name,b in variants.items():
    notice=('\n// T58.2 AUTHORED MODIFICATION: '+modifications[name]+'.\n'
            '// Modified 2026-10-18 from Apache-2.0 jaeger-client-go generated source\n'
            '// at 8d8e8fcfd04de42b8482476abac6a902fca47c18; see retained LICENSE.\n').encode()
    (ctxroot/(name+'.go')).write_bytes(b+notice)
public_context=[{'logical_path':str(p.relative_to(PUBLIC)), 'input':str(p.relative_to(ROOT))}
                for p in sorted((PUBLIC/'thrift-gen').rglob('*.go'))]
contexts={'public':public_context,'none':[]}
for name in variants:
    replacement={'logical_path':'thrift-gen/agent/agent.go','input':f'inputs/authored-context/{name}.go'}
    if name=='duplicate-agent':
        replacement['logical_path']='thrift-gen/otheragent/otheragent.go'
        contexts[name]=public_context+[replacement]
    else:
        # Do not allow any other actual generated service to establish Agent.
        contexts[name]=[replacement]
recipe={'prefix_utf8':'package probe\n//','repeat_ascii':'x','repeat_count':4194305-len(b'package probe\n//'),
        'suffix_utf8':'','bytes':4194305}
b=recipe['prefix_utf8'].encode()+b'x'*recipe['repeat_count']
recipe['sha256']=digest(b)
write_json(ROOT/'oversized-source.json',recipe)
cases.append({'id':'c26-oversized','kind':'file-gap','source':None,
              'source_recipe':'oversized-source.json','context':'public','sites':[],
              'required_file_outcome':{'predicate':'THRIFT_EXTRACTION_GAP','reason':'source_too_large','positive_claims':0}})
write_json(ROOT/'controls.json',{'schema':'t582-independent-authored-controls-v1',
 'origin':'Authored source controls; no candidate outputs used. Context variants are disclosed byte edits of licensed frozen public Agent Go; not generator executions.',
 'contexts':contexts,'cases':cases})
print(json.dumps({'public_calls':len(public_rows),'public_supported_calls':sum(x['role']=='supported-method-call' for x in public_rows),
 'public_supported_processor_constructions':sum(x['role']=='supported-processor-construction' for x in public_rows),
 'public_excluded_calls':sum(x['role']=='excluded-other-call' for x in public_rows),
 'generated_services':len(witnesses),'generated_methods':sum(len(x['methods']) for x in witnesses),
 'control_cases':len(cases),'control_sites':sum(len(x['sites']) for x in cases),
 'control_roles':{r:sum(s['role']==r for x in cases for s in x['sites']) for r in sorted({s['role'] for x in cases for s in x['sites']})}},indent=2))
