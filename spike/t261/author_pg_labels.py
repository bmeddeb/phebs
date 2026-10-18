"""Literal-span serializer for hand-authored PG choices; never parses SQL."""
from pathlib import Path
import hashlib
import json

ROOT=Path(__file__).resolve().parent
COMMIT="559b5021e418f12ded175f950b709b7fa66be5a5"


class Source:
    def __init__(self,path):
        self.path=path
        self.raw=(ROOT/"corpus/hatchet"/path).read_bytes()
        self.lines=self.raw.splitlines(keepends=True)
        self.offsets=[0]
        for line in self.lines:self.offsets.append(self.offsets[-1]+len(line))
        self.statements=[]

    def span(self,start,end):
        return dict(start=start,end=end,sha256=hashlib.sha256(self.raw[start:end]).hexdigest())

    def line_span(self,first,last):
        return self.span(self.offsets[first-1],self.offsets[last])

    def literal(self,line,literal,occurrence=0):
        raw=literal.encode()
        position=-1
        for _ in range(occurrence+1):
            position=self.lines[line-1].find(raw,position+1)
            assert position>=0,(self.path,line,literal,occurrence)
        return self.span(self.offsets[line-1]+position,self.offsets[line-1]+position+len(raw))

    def obj(self,kind,name,line,relation=None,**extra):
        quoted=name.startswith('"')
        literal=name
        if name.startswith("@"):
            literal={"primary_key":"PRIMARY KEY","unique":"UNIQUE","check":"CHECK","foreign_key":"FOREIGN KEY"}[kind]
            cite=self.literal(line,literal)
            name="@"+str(cite["start"])
        else:cite=self.literal(line,literal)
        value=dict(kind=kind,name=name.strip('"'),quoted=quoted,**cite)
        if relation is not None:value["relation"]=relation
        value.update(extra)
        return value

    def statement(self,name,first,last,syntax,disposition="declaration",objects=None,note=None):
        row=dict(id=name,**self.line_span(first,last),syntax_class=syntax,expected_disposition=disposition,objects=objects or [])
        if note:row["note"]=note
        self.statements.append(row)

    def table(self,name,first,last,columns,keys):
        objects=[self.obj("table",name,first)]
        for spec in columns.split():
            line,column=spec.split(":",1)
            objects.append(self.obj("column",column,int(line),relation=name))
        for kind,key,line,members in keys:
            objects.append(self.obj(kind,key,line,relation=name,columns=members.split()))
        self.statement("table-"+name,first,last,"create-table",objects=objects)

    def index(self,name,first,last,relation,members,note=None):
        self.statement("index-"+name,first,last,"create-index",objects=[self.obj("index",name,first,relation=relation,columns=members.split())],note=note)

    def enum(self,name,first,last):
        self.statement("enum-"+name,first,last,"create-enum",objects=[self.obj("enum",name,first)])

    def gap(self,first,last,syntax="create-function",disposition="procedural-sql",note=None):
        self.statement(f"gap-L{first}",first,last,syntax,disposition,note=note or "Procedural SQL is a frozen gap; bodies/dynamic identifiers supply no current declarations or authored-query sites.")

    def write(self,name,notes=None):
        self.statements.sort(key=lambda s:s["start"])
        value=dict(format="t261-source-labels-v1",dialect="postgresql",repository="hatchet-dev/hatchet",commit=COMMIT,source_path=self.path,source_sha256=hashlib.sha256(self.raw).hexdigest(),plane="sql-schema",statements=self.statements,inventory_notes=notes or [])
        dest=ROOT/"labels"/name
        dest.parent.mkdir(exist_ok=True)
        dest.write_text(json.dumps(value,indent=2,ensure_ascii=False)+"\n")
        counts={}
        for statement in self.statements:
            for obj in statement["objects"]:counts[obj["kind"]]=counts.get(obj["kind"],0)+1
        print(json.dumps(dict(path=str(dest),statements=len(self.statements),objects=counts,total=sum(counts.values()),sha256=hashlib.sha256(dest.read_bytes()).hexdigest())))


def core():
    s=Source("sql/schema/v1-core.sql")
    for first,last in [(1,18),(20,39),(41,77),(79,113),(116,130),(134,194),(249,326),(512,549),(978,1061),(1069,1189),(1191,1208),(1228,1382),(1390,1572),(1580,1714),(1722,1859),(1867,1888),(1995,2101),(2103,2164),(2166,2207),(2209,2271),(2273,2322),(2324,2417),(2470,2491),(2619,2639),(2647,2667),(2675,2697)]:s.gap(first,last)
    for first,last in [(328,331),(551,555),(1063,1067),(1210,1214),(1384,1388),(1574,1578),(1716,1720),(1861,1865),(1890,1894),(2493,2497),(2641,2645),(2669,2673),(2699,2703)]:s.gap(first,last,"create-trigger")
    for first,last in [(464,471),(582,589),(666,673),(699,706)]:s.gap(first,last,"alter-table-storage","wrapper","Committed table storage configuration; no additional current declaration object and no runtime interpretation.")
    for name,first,last in [("v1_sticky_strategy",205,205),("v1_task_initial_state",207,207),("v1_concurrency_strategy",211,211),("v1_task_event_type",393,399),("v1_match_kind",725,725),("v1_event_type",769,769),("v1_match_condition_action",775,775),("v1_incoming_webhook_auth_type",820,820),("v1_incoming_webhook_hmac_algorithm",821,821),("v1_incoming_webhook_hmac_encoding",822,822),("v1_incoming_webhook_source_name",825,825),("v1_log_line_level",1896,1896),("v1_step_match_condition_kind",1916,1916),("v1_payload_type",1944,1944),("v1_payload_location",1947,1947),("v1_durable_event_log_kind",2531,2535)]:s.enum(name,first,last)
    tables=[
    ("v1_queue",197,203,"198:tenant_id 199:name 200:last_active",[("primary_key","v1_queue_pkey",202,"tenant_id name")]),
    ("v1_workflow_concurrency",213,227,"216:id 217:workflow_id 218:workflow_version_id 220:is_active 221:strategy 222:child_strategy_ids 223:expression 224:tenant_id 225:max_concurrency",[("primary_key","v1_workflow_concurrency_pkey",226,"workflow_id workflow_version_id id")]),
    ("v1_step_concurrency",229,247,"232:id 234:parent_strategy_id 235:workflow_id 236:workflow_version_id 237:step_id 239:is_active 241:last_active_at 242:strategy 243:expression 244:tenant_id 245:max_concurrency",[("primary_key","v1_step_concurrency_pkey",246,"workflow_id workflow_version_id step_id id")]),
    ("v1_task",334,381,"335:id 336:inserted_at 337:tenant_id 338:queue 339:action_id 340:step_id 341:step_readable_id 342:workflow_id 343:workflow_version_id 344:workflow_run_id 345:schedule_timeout 346:step_timeout 347:priority 348:sticky 349:desired_worker_id 350:external_id 351:display_name 352:input 353:retry_count 354:internal_retry_count 355:app_retry_count 358:step_index 359:additional_metadata 360:dag_id 361:dag_inserted_at 362:parent_task_external_id 363:parent_task_id 364:parent_task_inserted_at 365:child_index 366:child_key 367:initial_state 368:initial_state_reason 369:concurrency_parent_strategy_ids 370:concurrency_strategy_ids 371:concurrency_keys 372:batch_key 373:retry_backoff_factor 374:retry_max_backoff 375:is_durable 376:desired_worker_label 377:triggering_event_external_id 378:triggering_event_key 379:idempotency_key",[("primary_key","v1_task_pkey",380,"id inserted_at")]),
    ("v1_lookup_table",383,391,"384:tenant_id 385:external_id 386:task_id 387:dag_id 388:inserted_at",[("primary_key","@",390,"external_id")]),
    ("v1_task_event",402,417,"403:id 404:inserted_at 405:tenant_id 406:task_id 407:task_inserted_at 408:retry_count 409:event_type 412:event_key 413:created_at 414:data 415:external_id",[("primary_key","v1_task_event_pkey",416,"task_id task_inserted_at id")]),
    ("v1_task_expression_eval",429,438,"430:key 431:task_id 432:task_inserted_at 433:value_str 434:value_int 435:kind",[("primary_key","v1_task_expression_eval_pkey",437,"task_id task_inserted_at kind key")]),
    ("v1_queue_item",442,462,"443:id 444:tenant_id 445:queue 446:task_id 447:task_inserted_at 448:external_id 449:action_id 450:step_id 451:workflow_id 452:workflow_run_id 453:schedule_timeout_at 454:step_timeout 455:priority 456:sticky 457:desired_worker_id 458:retry_count 459:desired_worker_label 460:batch_key",[("primary_key","v1_queue_item_pkey",461,"id")]),
    ("v1_task_runtime",487,501,"488:task_id 489:task_inserted_at 490:retry_count 491:worker_id 492:batch_id 493:batch_size 494:batch_index 495:batch_key 496:tenant_id 497:timeout_at 498:evicted_at",[("primary_key","v1_task_runtime_pkey",500,"task_id task_inserted_at retry_count")]),
    ("v1_batch_runtime",557,566,"558:tenant_id 559:step_id 560:action_id 561:batch_key 562:batch_id 563:started_at",[("primary_key","v1_batch_runtime_pkey",565,"tenant_id batch_id")]),
    ("v1_step_batch_config",572,580,"573:step_id 574:batch_max_size 575:batch_max_interval 576:batch_group_key 577:batch_group_max_runs 578:broadcast_output",[("primary_key","v1_step_batch_config_pkey",579,"step_id")]),
    ("v1_worker_slot_config",592,600,"593:tenant_id 594:worker_id 595:slot_type 596:max_units 597:created_at 598:updated_at",[("primary_key","@",599,"tenant_id worker_id slot_type")]),
    ("v1_step_slot_request",605,613,"606:tenant_id 607:step_id 608:slot_type 609:units 610:created_at 611:updated_at",[("primary_key","@",612,"tenant_id step_id slot_type")]),
    ("v1_task_runtime_slot",619,630,"620:tenant_id 621:task_id 622:task_inserted_at 623:retry_count 624:worker_id 625:slot_type 626:units 627:created_at 628:updated_at",[("primary_key","@",629,"task_id task_inserted_at retry_count slot_type")]),
    ("v1_rate_limited_queue_items",636,658,"637:requeue_after 639:tenant_id 640:queue 641:task_id 642:task_inserted_at 643:external_id 644:action_id 645:step_id 646:workflow_id 647:workflow_run_id 648:schedule_timeout_at 649:step_timeout 650:priority 651:sticky 652:desired_worker_id 653:retry_count 654:desired_worker_label 655:batch_key",[("primary_key","v1_rate_limited_queue_items_pkey",657,"task_id task_inserted_at retry_count")]),
    ("v1_batched_queue_item",675,697,"676:id 677:tenant_id 678:queue 679:task_id 680:task_inserted_at 681:external_id 682:action_id 683:step_id 684:workflow_id 685:workflow_run_id 686:schedule_timeout_at 687:step_timeout 688:priority 689:sticky 690:desired_worker_id 691:retry_count 692:batch_key 693:inserted_at 694:payload_size",[("primary_key","v1_batched_queue_item_pkey",695,"id"),("unique","v1_batched_queue_item_task_key",696,"task_id task_inserted_at retry_count")]),
    ("v1_match",727,767,"728:id 729:tenant_id 730:kind 731:is_satisfied 736:existing_data 737:signal_task_id 738:signal_task_inserted_at 739:signal_task_external_id 740:signal_external_id 741:signal_key 743:trigger_dag_id 744:trigger_dag_inserted_at 746:trigger_step_id 747:trigger_step_index 749:trigger_external_id 750:trigger_workflow_run_id 751:trigger_parent_task_external_id 752:trigger_parent_task_id 753:trigger_parent_task_inserted_at 754:trigger_child_index 755:trigger_child_key 757:trigger_existing_task_id 758:trigger_existing_task_inserted_at 759:trigger_priority 760:trigger_event_external_id 761:trigger_event_key 762:trigger_desired_worker_labels 764:durable_event_log_entry_node_id 765:durable_event_log_entry_branch_id",[("primary_key","v1_match_pkey",766,"id")]),
    ("v1_match_condition",777,794,"778:v1_match_id 779:id 780:tenant_id 781:registered_at 782:event_type 784:event_key 785:event_resource_hint 787:readable_data_key 788:is_satisfied 789:action 790:or_group_id 791:expression 792:data",[("primary_key","v1_match_condition_pkey",793,"v1_match_id id")]),
    ("v1_filter",796,810,"797:id 798:tenant_id 799:workflow_id 800:scope 801:expression 802:payload 803:payload_hash 804:is_declarative 806:inserted_at 807:updated_at",[("primary_key","@",809,"tenant_id id")]),
    ("v1_incoming_webhook",827,890,"828:tenant_id 831:name 833:source_name 837:event_key_expression 838:scope_expression 839:static_payload 840:return_event_as_response_payload 842:auth_method 844:auth__basic__username 845:auth__basic__password 847:auth__api_key__header_name 848:auth__api_key__key 850:auth__hmac__algorithm 851:auth__hmac__encoding 852:auth__hmac__signature_header_name 853:auth__hmac__webhook_signing_secret 855:inserted_at 856:updated_at",[("primary_key","@",858,"tenant_id name"),("check","@",859,""),("check","@",886,""),("check","@",888,""),("check","@",889,"")]),
    ("v1_dag",901,913,"902:id 903:inserted_at 904:tenant_id 905:external_id 906:display_name 907:workflow_id 908:workflow_version_id 909:parent_task_external_id 910:desired_worker_labels 911:idempotency_key",[("primary_key","v1_dag_pkey",912,"id inserted_at")]),
    ("v1_dag_to_task",915,921,"916:dag_id 917:dag_inserted_at 918:task_id 919:task_inserted_at",[("primary_key","v1_dag_to_task_pkey",920,"dag_id dag_inserted_at task_id task_inserted_at")]),
    ("v1_dag_data",923,929,"924:dag_id 925:dag_inserted_at 926:input 927:additional_metadata",[("primary_key","v1_dag_input_pkey",928,"dag_id dag_inserted_at")]),
    ("v1_workflow_concurrency_slot",932,946,"933:sort_id 934:tenant_id 935:workflow_id 936:workflow_version_id 937:workflow_run_id 938:strategy_id 940:completed_child_strategy_ids 941:child_strategy_ids 942:priority 943:key 944:is_filled",[("primary_key","v1_workflow_concurrency_slot_pkey",945,"strategy_id workflow_version_id workflow_run_id")]),
    ("v1_concurrency_slot",951,972,"952:sort_id 953:task_id 954:task_inserted_at 955:task_retry_count 956:external_id 957:tenant_id 958:workflow_id 959:workflow_version_id 960:workflow_run_id 961:strategy_id 962:parent_strategy_id 963:priority 964:key 965:is_filled 966:next_parent_strategy_ids 967:next_strategy_ids 968:next_keys 969:queue_to_notify 970:schedule_timeout_at",[("primary_key","v1_concurrency_slot_pkey",971,"task_id task_inserted_at task_retry_count strategy_id")]),
    ("v1_retry_queue_item",1216,1224,"1217:task_id 1218:task_inserted_at 1219:task_retry_count 1220:retry_after 1221:tenant_id",[("primary_key","v1_retry_queue_item_pkey",1223,"task_id task_inserted_at task_retry_count")]),
    ("v1_log_line",1898,1912,"1899:id 1900:created_at 1901:tenant_id 1902:task_id 1903:task_inserted_at 1904:message 1905:level 1906:metadata 1907:retry_count 1908:workflow_id 1909:step_id",[("primary_key","@",1911,"task_id task_inserted_at id")]),
    ("v1_step_match_condition",1918,1934,"1919:id 1920:tenant_id 1921:step_id 1922:readable_data_key 1923:action 1924:or_group_id 1925:expression 1926:kind 1928:sleep_duration 1930:event_key 1932:parent_readable_id",[("primary_key","@",1933,"step_id id")]),
    ("v1_durable_sleep",1936,1942,"1937:id 1938:tenant_id 1939:sleep_until 1940:sleep_duration",[("primary_key","@",1941,"tenant_id sleep_until id")]),
    ("v1_payload",1949,1966,"1950:tenant_id 1951:id 1952:inserted_at 1953:external_id 1954:type 1955:location 1956:external_location_key 1957:inline_content 1958:updated_at",[("primary_key","@",1960,"tenant_id inserted_at id type"),("check","@",1961,"")]),
    ("v1_payload_cutover_job_offset",1970,1977,"1971:key 1972:is_completed 1973:lease_process_id 1974:lease_expires_at 1976:last_external_id",[("primary_key","@",1971,"key")]),
    ("v1_payload_offloaded_block_index",1985,1991,"1986:payload_inserted_at_date 1987:block_external_id_range 1988:index_file_key",[("exclusion","v1_payload_offloaded_block_index_date_range_excl",1989,"payload_inserted_at_date block_external_id_range")]),
    ("v1_idempotency_key",2419,2431,"2420:tenant_id 2422:key 2424:expires_at 2425:claimed_by_external_id 2427:inserted_at 2428:updated_at",[("primary_key","@",2430,"tenant_id key")]),
    ("v1_operation_interval_settings",2437,2443,"2438:tenant_id 2439:operation_id 2441:interval_nanoseconds",[("primary_key","@",2442,"tenant_id operation_id")]),
    ("v1_event",2446,2457,"2447:id 2448:seen_at 2449:tenant_id 2450:external_id 2451:key 2452:additional_metadata 2453:scope 2454:triggering_webhook_name",[("primary_key","@",2456,"tenant_id seen_at id")]),
    ("v1_event_lookup_table",2461,2468,"2462:tenant_id 2463:external_id 2464:event_id 2465:event_seen_at",[("primary_key","@",2467,"external_id event_seen_at")]),
    ("v1_event_to_run",2499,2506,"2500:run_external_id 2501:event_id 2502:event_seen_at 2503:filter_id",[("primary_key","@",2505,"event_id event_seen_at run_external_id")]),
    ("v1_durable_event_log_file",2512,2529,"2513:tenant_id 2516:durable_task_id 2517:durable_task_inserted_at 2519:latest_invocation_count 2521:latest_inserted_at 2524:latest_node_id 2526:latest_branch_id",[("primary_key","v1_durable_event_log_file_pkey",2528,"durable_task_id durable_task_inserted_at")]),
    ("v1_durable_event_log_entry",2537,2579,"2538:tenant_id 2540:external_id 2541:result_payload_external_id 2543:child_task_external_id 2544:child_task_is_failure 2545:child_task_error_message 2550:inserted_at 2551:id 2553:durable_task_id 2554:durable_task_inserted_at 2556:kind 2559:node_id 2561:branch_id 2564:idempotency_key 2572:is_satisfied 2573:satisfied_at 2575:user_message 2576:wait_data",[("primary_key","v1_durable_event_log_entry_pkey",2578,"durable_task_id durable_task_inserted_at branch_id node_id")]),
    ("v1_durable_event_log_branch_point",2582,2600,"2583:tenant_id 2585:id 2587:inserted_at 2589:durable_task_id 2591:durable_task_inserted_at 2593:first_node_id_in_new_branch 2595:parent_branch_id 2597:next_branch_id",[("primary_key","v1_durable_event_log_branch_point_pkey",2599,"durable_task_id durable_task_inserted_at parent_branch_id first_node_id_in_new_branch next_branch_id")]),
    ("tenant_entitlement",2602,2617,"2603:tenant_id 2605:audit_logs 2607:prometheus_metrics 2611:strict_additional_metadata_filters 2613:created_at 2614:updated_at",[("primary_key","tenant_entitlement_pkey",2616,"tenant_id")]),
    ]
    for args in tables:s.table(*args)
    indexes=[
    ("v1_task_event_event_key_unique_idx",420,426,"v1_task_event","tenant_id task_id task_inserted_at event_type event_key"),
    ("v1_queue_item_list_idx",473,478,"v1_queue_item","tenant_id queue priority id"),
    ("v1_queue_item_task_idx",480,484,"v1_queue_item","task_id task_inserted_at retry_count"),
    ("v1_task_runtime_tenantId_workerId_idx",503,503,"v1_task_runtime","tenant_id worker_id"),
    ("v1_task_runtime_tenantId_timeoutAt_idx",505,505,"v1_task_runtime","tenant_id timeout_at"),
    ("v1_task_runtime_tenant_worker_not_evicted_idx",507,507,"v1_task_runtime","tenant_id worker_id"),
    ("v1_task_runtime_batch_id_idx",509,509,"v1_task_runtime","batch_id"),
    ("v1_batch_runtime_key_idx",568,569,"v1_batch_runtime","tenant_id step_id batch_key"),
    ("v1_worker_slot_config_worker_id_idx",602,602,"v1_worker_slot_config","worker_id"),
    ("v1_step_slot_request_step_idx",615,616,"v1_step_slot_request","step_id"),
    ("v1_task_runtime_slot_tenant_worker_type_idx",632,633,"v1_task_runtime_slot","tenant_id worker_id slot_type"),
    ("v1_rate_limited_queue_items_tenant_requeue_after_idx",660,664,"v1_rate_limited_queue_items","tenant_id queue requeue_after"),
    ("v1_batched_queue_item_step_batch_id_idx",709,714,"v1_batched_queue_item","tenant_id step_id batch_key id"),
    ("v1_batched_queue_item_step_priority_idx",718,723,"v1_batched_queue_item","tenant_id step_id priority id"),
    ("v1_filter_unique_tenant_workflow_id_scope_expression_payload",812,818,"v1_filter","tenant_id workflow_id scope expression payload_hash"),
    ("v1_match_condition_filter_idx",893,899,"v1_match_condition","tenant_id event_type event_key is_satisfied event_resource_hint"),
    ("v1_workflow_concurrency_slot_query_idx",948,948,"v1_workflow_concurrency_slot","tenant_id strategy_id key priority sort_id"),
    ("v1_concurrency_slot_query_idx",974,974,"v1_concurrency_slot","tenant_id strategy_id key sort_id"),
    ("v1_retry_queue_item_tenant_id_retry_after_idx",1226,1226,"v1_retry_queue_item","tenant_id retry_after"),
    ("v1_log_line_tenant_id_level_idx",1914,1914,"v1_log_line","tenant_id created_at level"),
    ("v1_payload_external_id_idx",1968,1968,"v1_payload","external_id"),
    ("v1_payload_offloaded_block_index_uq_index_key",1993,1993,"v1_payload_offloaded_block_index","index_file_key"),
    ("v1_idempotency_key_expires_at_idx",2433,2433,"v1_idempotency_key","tenant_id expires_at"),
    ("v1_event_key_scope_idx",2459,2459,"v1_event","tenant_id key scope"),
    ]
    for args in indexes:s.index(*args,note="Explicit index order is retained; direction/predicate/operator-class spelling remains citable in complete statement bytes. A UNIQUE INDEX is an explicit index, not a separate UNIQUE constraint.")
    s.gap(1979,1979,"create-extension","unsupported-statement","Extension declaration is retained as an explicit unsupported statement; no extension/runtime behavior is inferred.")
    s.statement("range-uuidrange",1981,1983,"create-range-type",objects=[s.obj("range_type","uuidrange",1981)])
    s.write("hatchet-v1-core.json",["Complete source-only current-schema input; function/trigger bodies and dynamic EXECUTE never become current declarations or authored-query usage.","Partition bounds, generated/identity expressions, EXCLUDE/GIST and range type remain explicit citable syntax; no live partitions or implicit sequence objects are invented."])


def olap():
    s=Source("sql/schema/v1-olap.sql")
    for first,last in [(15,25),(27,37),(41,101),(104,121),(123,154),(156,194),(365,387),(478,500),(502,527),(529,554),(611,677),(685,701),(709,726),(734,783),(791,806),(814,831),(839,864),(875,907),(915,936),(953,1059),(1061,1118),(1120,1182),(1184,1230),(1232,1273),(1275,1368)]:s.gap(first,last)
    for first,last in [(679,683),(703,707),(728,732),(785,789),(808,812),(833,837),(866,870),(909,913),(938,942)]:s.gap(first,last,"create-trigger")
    for name,first,last in [("v1_sticky_strategy_olap",1,1),("v1_readable_status_olap",3,10),("v1_run_kind",257,257),("v1_status_kind",304,304),("v1_event_type_olap",319,346),("v1_payload_location_olap",432,432),("v1_cel_evaluation_failure_source",593,593),("v1_otel_span_kind",1370,1370),("v1_otel_status_code",1372,1372)]:s.enum(name,first,last)
    tables=[
    ("v1_tasks_olap",197,226,"198:tenant_id 199:id 200:inserted_at 201:external_id 202:queue 203:action_id 204:step_id 205:workflow_id 206:workflow_version_id 207:workflow_run_id 208:schedule_timeout 209:step_timeout 210:priority 211:sticky 212:desired_worker_id 213:display_name 214:input 215:additional_metadata 216:readable_status 217:latest_retry_count 218:latest_worker_id 219:dag_id 220:dag_inserted_at 221:parent_task_external_id 222:is_durable 223:idempotency_key",[("primary_key","@",225,"inserted_at id")]),
    ("v1_dags_olap",237,252,"238:id 239:inserted_at 240:tenant_id 241:external_id 242:display_name 243:workflow_id 244:workflow_version_id 245:readable_status 246:input 247:additional_metadata 248:parent_task_external_id 249:total_tasks 250:idempotency_key",[("primary_key","@",251,"inserted_at id")]),
    ("v1_runs_olap",260,274,"261:tenant_id 262:id 263:inserted_at 264:external_id 265:readable_status 266:kind 267:workflow_id 268:workflow_version_id 269:additional_metadata 270:parent_task_external_id 271:idempotency_key",[("primary_key","@",273,"inserted_at id")]),
    ("v1_lookup_table_olap",285,293,"286:tenant_id 287:external_id 288:task_id 289:dag_id 290:inserted_at",[("primary_key","@",292,"external_id")]),
    ("v1_dag_to_task_olap",295,301,"296:dag_id 297:dag_inserted_at 298:task_id 299:task_inserted_at",[("primary_key","@",300,"dag_id dag_inserted_at task_id task_inserted_at")]),
    ("v1_statuses_olap",306,315,"307:external_id 308:inserted_at 309:tenant_id 310:workflow_id 311:kind 312:readable_status",[("primary_key","@",314,"external_id inserted_at")]),
    ("v1_task_events_olap_tmp",350,363,"351:tenant_id 352:requeue_after 353:requeue_retries 354:id 355:task_id 356:task_inserted_at 357:event_type 358:readable_status 359:retry_count 360:worker_id",[("primary_key","@",362,"tenant_id requeue_after task_id id")]),
    ("v1_task_events_olap",389,409,"390:tenant_id 391:id 392:inserted_at 393:external_id 394:task_id 395:task_inserted_at 396:event_type 397:workflow_id 398:event_timestamp 399:readable_status 400:retry_count 401:error_message 402:output 403:worker_id 404:additional__event_data 405:additional__event_message 406:durable_invocation_count",[("primary_key","@",408,"task_id task_inserted_at id")]),
    ("v1_incoming_webhook_validation_failures_olap",413,427,"414:id 416:tenant_id 419:incoming_webhook_name 421:error 423:inserted_at 424:updated_at",[("primary_key","@",426,"inserted_at id")]),
    ("v1_payloads_olap",434,451,"435:tenant_id 436:external_id 438:location 439:external_location_key 440:inline_content 442:inserted_at 443:updated_at",[("primary_key","@",445,"tenant_id external_id inserted_at"),("check","@",446,"")]),
    ("v1_payloads_olap_offloaded_block_index",455,461,"456:payload_inserted_at_date 457:block_external_id_range 458:index_file_key",[("exclusion","v1_payloads_olap_offloaded_block_index_date_range_excl",459,"payload_inserted_at_date block_external_id_range")]),
    ("v1_task_status_updates_tmp",467,476,"468:tenant_id 469:requeue_after 470:requeue_retries 471:id 472:dag_id 473:dag_inserted_at",[("primary_key","@",475,"tenant_id requeue_after dag_id id")]),
    ("v1_events_olap",557,569,"558:tenant_id 559:id 560:external_id 561:seen_at 562:key 563:payload 564:additional_metadata 565:scope 566:triggering_webhook_name",[("primary_key","@",568,"tenant_id seen_at id")]),
    ("v1_event_lookup_table_olap",574,581,"575:tenant_id 576:external_id 577:event_id 578:event_seen_at",[("primary_key","@",580,"tenant_id external_id event_seen_at")]),
    ("v1_event_to_run_olap",583,591,"584:run_id 585:run_inserted_at 586:event_id 587:event_seen_at 588:filter_id",[("primary_key","@",590,"event_id event_seen_at run_id run_inserted_at")]),
    ("v1_cel_evaluation_failures_olap",595,608,"596:id 598:tenant_id 600:source 602:error 604:inserted_at 605:updated_at",[("primary_key","@",607,"inserted_at id")]),
    ("v1_payloads_olap_cutover_job_offset",944,951,"945:key 946:is_completed 947:lease_process_id 948:lease_expires_at 950:last_external_id",[("primary_key","@",945,"key")]),
    ("v1_otel_trace_olap",1374,1394,"1375:tenant_id 1376:trace_id 1377:span_id 1378:parent_span_id 1379:span_name 1380:span_kind 1381:service_name 1382:status_code 1383:status_message 1384:duration_ns 1385:resource_attributes 1386:span_attributes 1387:scope_name 1388:scope_version 1389:task_run_external_id 1390:workflow_run_external_id 1391:retry_count 1392:start_time",[("primary_key","@",1393,"tenant_id trace_id start_time span_id")]),
    ("v1_otel_trace_lookup_olap",1404,1411,"1405:tenant_id 1406:external_id 1407:retry_count 1408:trace_id 1409:start_time",[("primary_key","@",1410,"tenant_id external_id retry_count start_time")]),
    ]
    for args in tables:s.table(*args)
    indexes=[
    ("v1_tasks_olap_workflow_id_idx",228,228,"v1_tasks_olap","tenant_id workflow_id"),
    ("v1_tasks_olap_worker_id_idx",229,229,"v1_tasks_olap","tenant_id latest_worker_id"),
    ("ix_v1_tasks_olap_idempotency_key",230,230,"v1_tasks_olap","idempotency_key inserted_at"),
    ("ix_v1_tasks_olap_additional_metadata_gin",234,234,"v1_tasks_olap","additional_metadata"),
    ("v1_dags_olap_workflow_id_idx",254,254,"v1_dags_olap","tenant_id workflow_id"),
    ("ix_v1_runs_olap_parent_task_external_id",276,276,"v1_runs_olap","parent_task_external_id"),
    ("ix_v1_runs_olap_tenant_ins_at_status",277,277,"v1_runs_olap","tenant_id inserted_at readable_status"),
    ("ix_v1_runs_olap_idempotency_key",278,278,"v1_runs_olap","idempotency_key inserted_at"),
    ("ix_v1_runs_olap_additional_metadata_gin",282,282,"v1_runs_olap","additional_metadata"),
    ("v1_task_events_olap_task_id_idx",411,411,"v1_task_events_olap","task_id"),
    ("v1_incoming_webhook_validation_failures_olap_tenant_id_incoming_webhook_name_idx",429,429,"v1_incoming_webhook_validation_failures_olap","tenant_id incoming_webhook_name"),
    ("v1_payloads_olap_external_id_idx",453,453,"v1_payloads_olap","external_id"),
    ("v1_payloads_olap_offloaded_block_index_uq_index_key",463,463,"v1_payloads_olap_offloaded_block_index","index_file_key"),
    ("v1_events_olap_key_idx",571,571,"v1_events_olap","tenant_id key"),
    ("v1_events_olap_scope_idx",572,572,"v1_events_olap","tenant_id scope"),
    ("idx_v1_otel_trace_olap_task_lookup",1396,1398,"v1_otel_trace_olap","tenant_id task_run_external_id"),
    ("idx_v1_otel_trace_olap_workflow_lookup",1400,1402,"v1_otel_trace_olap","tenant_id workflow_run_external_id"),
    ]
    for args in indexes:s.index(*args,note="Explicit index ordering retained; complete source preserves ASC/DESC, partial predicates and GIN/operator-class syntax. These are declarations only.")
    s.write("hatchet-v1-olap.json",["Complete authored schema input; function/trigger bodies (including LANGUAGE sql) are explicit procedural gaps and never primary authored-query evidence.","All explicitly written table/column/key/index/enum/exclusion objects remain in the declaration denominator; implicit identity sequences and dynamic partitions are not invented."])


def stubs():
    s=Source("sql/schema/pg-stubs.sql")
    for name,first,last,columns in [
    ("pg_stat_user_tables",3,29,"5:relid 6:schemaname 7:relname 8:seq_scan 9:seq_tup_read 10:idx_scan 11:idx_tup_fetch 12:n_tup_ins 13:n_tup_upd 14:n_tup_del 15:n_tup_hot_upd 16:n_live_tup 17:n_dead_tup 18:n_mod_since_analyze 19:n_ins_since_vacuum 20:vacuum_count 21:autovacuum_count 22:analyze_count 23:autoanalyze_count 24:last_vacuum 25:last_autovacuum 26:last_analyze 27:last_autoanalyze"),
    ("pg_statio_user_tables",31,45,"33:relid 34:schemaname 35:relname 36:heap_blks_read 37:heap_blks_hit 38:idx_blks_read 39:idx_blks_hit 40:toast_blks_read 41:toast_blks_hit 42:tidx_blks_read 43:tidx_blks_hit"),
    ("pg_available_extensions",47,54,"49:name 50:default_version 51:installed_version 52:comment"),
    ("pg_stat_statements",56,78,"58:userid 59:dbid 60:queryid 61:query 62:calls 63:total_exec_time 64:rows 65:shared_blks_hit 66:shared_blks_read 67:shared_blks_dirtied 68:shared_blks_written 69:local_blks_hit 70:local_blks_read 71:local_blks_dirtied 72:local_blks_written 73:temp_blks_read 74:temp_blks_written 75:blk_read_time 76:blk_write_time"),
    ("pg_stat_progress_vacuum",80,95,"82:pid 83:datid 84:datname 85:relid 86:phase 87:heap_blks_total 88:heap_blks_scanned 89:heap_blks_vacuumed 90:heap_blks_frozen 91:index_vacuum_count 92:max_dead_tuples 93:num_dead_tuples"),
    ("pg_stat_activity",97,109,"99:pid 100:usename 101:application_name 102:client_addr 103:state 104:query_start 105:wait_event_type 106:wait_event 107:query"),
    ]:
        objects=[s.obj("view",name,first,source_role="sqlc-tooling-stub")]
        for spec in columns.split():
            line,column=spec.split(":",1)
            # The author chooses the AS output, including the `name` case where
            # the same token also appears in the NULL::name source type.
            cite=s.literal(int(line)," AS "+column)
            cite=s.span(cite["start"]+4,cite["end"])
            objects.append(dict(kind="view_column",name=column,quoted=False,**cite,relation=name,source_role="sqlc-tooling-stub"))
        s.statement("view-"+name,first,last,"create-view",objects=objects,note="Explicit NULL/WHERE false sqlc tooling model; never a real pg_catalog or live PostgreSQL declaration.")
    s.write("hatchet-pg-stubs.json",["Complete committed sqlc stub models; exact source role must accompany catalog facts."])
    s=Source("pkg/repository/sqlcv1/concurrency-additional-tables.sql")
    s.statement("table-tmp_workflow_concurrency_slot",2,2,"create-table-like",objects=[s.obj("table","tmp_workflow_concurrency_slot",2,source_role="sqlc-tooling-model")],note="Source explicitly says never written to schema. LIKE ... INCLUDING ALL references v1_workflow_concurrency_slot; no copied columns/keys/indexes or live table are inferred. Expansion is a frozen parser gap.")
    s.write("hatchet-additional-tables.json",["Complete authored sqlc model; no recursive/cross-blob LIKE expansion under one-blob citation contract."])


if __name__=="__main__":
    core()
    olap()
    stubs()
