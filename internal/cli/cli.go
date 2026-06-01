package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nathan/hebb/internal/agent"
	"github.com/nathan/hebb/internal/associate"
	"github.com/nathan/hebb/internal/config"
	"github.com/nathan/hebb/internal/embed"
	"github.com/nathan/hebb/internal/encode"
	"github.com/nathan/hebb/internal/mcp"
	"github.com/nathan/hebb/internal/memory"
	"github.com/nathan/hebb/internal/store"
)

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	cmd, args := canonicalCommand(args[0]), args[1:]
	switch cmd {
	case "init":
		return runInit(args, stdout)
	case "doctor":
		return runDoctor(args, stdout)
	case "encode":
		return runEncode(args, stdout)
	case "retrieve":
		return runRetrieve(args, stdout)
	case "associate":
		return runAssociate(args, stdout)
	case "reinforce":
		return runTraceAction(args, stdout, "reinforce")
	case "inhibit":
		return runTraceAction(args, stdout, "inhibit")
	case "forget":
		return runForget(args, stdout)
	case "consolidate":
		return runConsolidate(args, stdout)
	case "inspect":
		return runInspect(args, stdout)
	case "maintain":
		return runMaintain(args, stdout)
	case "mcp":
		return runMCP(args, stdout)
	case "agent":
		return runAgent(args, stdout)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func canonicalCommand(cmd string) string {
	switch cmd {
	case "remember":
		return "encode"
	case "recall":
		return "retrieve"
	case "link":
		return "associate"
	default:
		return cmd
	}
}

func commonFlags(name string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	home := fs.String("home", "", "override Hebb home directory")
	db := fs.String("db", "", "override Hebb SQLite database path")
	return fs, home, db
}

func openStore(home, db string) (*store.Store, config.Paths, error) {
	paths, err := config.Resolve(home, db)
	if err != nil {
		return nil, config.Paths{}, err
	}
	s, err := store.Open(store.OpenOptions{Home: paths.Home, DB: paths.DB})
	return s, paths, err
}

func runInit(args []string, stdout io.Writer) error {
	fs, home, db := commonFlags("init")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, paths, err := openStore(*home, *db)
	if err != nil {
		return err
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status := "ollama unavailable; vector table will be created after maintain embed --pending"
	if vector, err := embed.NewClient("", "").Embed(ctx, "hebb embedding dimension probe"); err == nil {
		if err := s.EnsureVectorTable(len(vector)); err != nil {
			return err
		}
		status = fmt.Sprintf("embedding_dimensions: %d", len(vector))
	}
	if err := s.SetSetting("hebb_home", paths.Home); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Hebb initialized\nhome: %s\ndb: %s\n%s\n", paths.Home, paths.DB, status)
	return nil
}

func runDoctor(args []string, stdout io.Writer) error {
	fs, home, db := commonFlags("doctor")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, paths, err := openStore(*home, *db)
	if err != nil {
		return err
	}
	defer s.Close()
	fmt.Fprintf(stdout, "home: %s\n", paths.Home)
	fmt.Fprintf(stdout, "db: %s\n", paths.DB)

	sqliteVersion, vecVersion, err := s.SQLiteVersions()
	if err != nil {
		fmt.Fprintf(stdout, "sqlite: unavailable (%v)\n", err)
	} else {
		fmt.Fprintf(stdout, "sqlite_version: %s\nvec_version: %s\n", sqliteVersion, vecVersion)
	}
	if dim, ok, err := s.EmbeddingDimensions(); err == nil && ok {
		fmt.Fprintf(stdout, "embedding_model: %s\nembedding_dimensions: %d\n", config.DefaultEmbeddingModel, dim)
	} else {
		fmt.Fprintf(stdout, "embedding_model: %s\nembedding_dimensions: unset\n", config.DefaultEmbeddingModel)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	vec, err := embed.NewClient("", "").Embed(ctx, "hebb doctor")
	if err != nil {
		fmt.Fprintf(stdout, "ollama: unavailable (%v)\n", err)
		return nil
	}
	fmt.Fprintf(stdout, "ollama: ok\nollama_dimensions: %d\n", len(vec))
	return nil
}

func runEncode(args []string, stdout io.Writer) error {
	fs, home, db := commonFlags("encode")
	kind := fs.String("kind", string(memory.TraceObservation), "trace kind")
	title := fs.String("title", "", "trace title")
	body := fs.String("body", "", "trace body")
	scope := fs.String("scope", "", "trace scope")
	source := fs.String("source", "", "trace source")
	metadata := fs.String("metadata", "{}", "metadata JSON")
	var entities repeatedFlag
	fs.Var(&entities, "entity", "entity name to link; may be repeated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	trace, err := encode.Normalize(encode.Request{
		Kind: memory.TraceKind(*kind), Title: *title, Body: *body, Scope: *scope, Source: *source,
	})
	if err != nil {
		return err
	}
	s, _, err := openStore(*home, *db)
	if err != nil {
		return err
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	vector := embedIfPossible(ctx, s, trace.Title+"\n"+trace.Body)
	id, err := s.CreateTrace(ctx, store.TraceInput{
		Kind: trace.Kind, Title: trace.Title, Body: trace.Body, Scope: trace.Scope, Source: trace.Source,
		Confidence: trace.Confidence, Strength: trace.Strength, Salience: trace.Salience, Status: trace.Status, MetadataJSON: *metadata,
	}, vector)
	if err != nil {
		return err
	}
	for _, entityName := range entities {
		entityID, err := s.UpsertEntity(ctx, entityName, "concept")
		if err != nil {
			return err
		}
		if err := s.LinkTraceEntity(ctx, id, entityID, "mentions", 0.7); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "trace encoded\nid: %d\nkind: %s\ntitle: %s\nembedding_pending: %t\n", id, trace.Kind, trace.Title, len(vector) == 0)
	return nil
}

func runRetrieve(args []string, stdout io.Writer) error {
	fs, home, db := commonFlags("retrieve")
	scope := fs.String("scope", "", "filter by scope")
	kind := fs.String("kind", "", "filter by kind")
	status := fs.String("status", "", "filter by status")
	limit := fs.Int("limit", 10, "maximum result count")
	jsonOut := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	query := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("retrieve query is required")
	}
	s, _, err := openStore(*home, *db)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	vector := embedIfPossible(ctx, s, query)
	results, err := s.Retrieve(ctx, store.RetrieveOptions{Query: query, Scope: *scope, Kind: *kind, Status: *status, Limit: *limit, Vector: vector})
	if err != nil {
		return err
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(results)
	}
	for _, result := range results {
		fmt.Fprintf(stdout, "[%d] %.3f %s %s\n%s\n\n", result.Trace.ID, result.Score, result.Trace.Kind, result.Trace.Title, result.Trace.Body)
	}
	return nil
}

func embedIfPossible(ctx context.Context, s *store.Store, text string) []float32 {
	vector, err := embed.NewClient("", "").Embed(ctx, text)
	if err != nil || len(vector) == 0 {
		return nil
	}
	_ = s.EnsureVectorTable(len(vector))
	return vector
}

func runAssociate(args []string, stdout io.Writer) error {
	relation, home, db, positional, err := parseRelationArgs(args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		return fmt.Errorf("usage: hebb associate <trace-id-a> <trace-id-b> --relation <relation>")
	}
	req := associate.Request{Relation: relation}
	if req.FromTraceID, err = parseID(positional[0]); err != nil {
		return err
	}
	if req.ToTraceID, err = parseID(positional[1]); err != nil {
		return err
	}
	if err := associate.Validate(req); err != nil {
		return err
	}
	s, _, err := openStore(home, db)
	if err != nil {
		return err
	}
	defer s.Close()
	id, err := s.Associate(context.Background(), req.FromTraceID, req.ToTraceID, req.Relation, 0.5, 0.7)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "association stored\nid: %d\nfrom: %d\nto: %d\nrelation: %s\n", id, req.FromTraceID, req.ToTraceID, req.Relation)
	return nil
}

func runTraceAction(args []string, stdout io.Writer, action string) error {
	home, db, reason, positional, err := parseCommonArgs(args, map[string]*string{"reason": nil})
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("usage: hebb %s <trace-id> --reason <reason>", action)
	}
	id, err := parseID(positional[0])
	if err != nil {
		return err
	}
	s, _, err := openStore(home, db)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx := context.Background()
	if action == "reinforce" {
		err = s.Reinforce(ctx, id, reason)
	} else {
		err = s.Inhibit(ctx, id, reason)
	}
	if err != nil {
		return err
	}
	past := "reinforced"
	if action == "inhibit" {
		past = "inhibited"
	}
	fmt.Fprintf(stdout, "trace %s\nid: %d\n", past, id)
	return nil
}

func runForget(args []string, stdout io.Writer) error {
	home, db, reason, positional, bools, err := parseForgetArgs(args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("usage: hebb forget <trace-id> --soft")
	}
	if bools["hard"] && !bools["yes"] {
		return fmt.Errorf("hard delete requires --yes")
	}
	id, err := parseID(positional[0])
	if err != nil {
		return err
	}
	s, _, err := openStore(home, db)
	if err != nil {
		return err
	}
	defer s.Close()
	soft := bools["soft"] && !bools["hard"]
	if err := s.Forget(context.Background(), id, soft, reason); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "trace forgotten\nid: %d\nsoft: %t\n", id, soft)
	return nil
}

func runConsolidate(args []string, stdout io.Writer) error {
	fs, home, db := commonFlags("consolidate")
	scope := fs.String("scope", "", "scope to consolidate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, _, err := openStore(*home, *db)
	if err != nil {
		return err
	}
	defer s.Close()
	id, err := s.Consolidate(context.Background(), *scope)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "consolidated\nsummary_trace_id: %d\nscope: %s\n", id, *scope)
	return nil
}

func runInspect(args []string, stdout io.Writer) error {
	fs, home, db := commonFlags("inspect")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: hebb inspect trace <trace-id> | hebb inspect stats")
	}
	s, _, err := openStore(*home, *db)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx := context.Background()
	switch fs.Arg(0) {
	case "trace":
		if fs.NArg() != 2 {
			return fmt.Errorf("usage: hebb inspect trace <trace-id>")
		}
		id, err := parseID(fs.Arg(1))
		if err != nil {
			return err
		}
		trace, err := s.GetTrace(ctx, id)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(trace)
	case "stats":
		stats, err := s.Stats(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(stats)
	case "entity":
		if fs.NArg() != 2 {
			return fmt.Errorf("usage: hebb inspect entity <entity-name>")
		}
		info, err := s.EntityInfo(ctx, fs.Arg(1))
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(info)
	default:
		return fmt.Errorf("unsupported inspect target %q", fs.Arg(0))
	}
}

func runMaintain(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hebb maintain embed --pending | hebb maintain decay --dry-run")
	}
	switch args[0] {
	case "embed":
		return runMaintainEmbed(args[1:], stdout)
	case "decay":
		return runMaintainDecay(args[1:], stdout)
	default:
		return fmt.Errorf("unknown maintain command %q", args[0])
	}
}

func runMaintainEmbed(args []string, stdout io.Writer) error {
	fs, home, db := commonFlags("maintain embed")
	pending := fs.Bool("pending", true, "embed traces pending vectors")
	limit := fs.Int("limit", 50, "maximum traces to embed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*pending {
		return fmt.Errorf("only --pending is supported")
	}
	s, _, err := openStore(*home, *db)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	traces, err := s.PendingEmbeddingTraces(ctx, *limit)
	if err != nil {
		return err
	}
	client := embed.NewClient("", "")
	var embedded int
	for _, trace := range traces {
		vector, err := client.Embed(ctx, trace.Title+"\n"+trace.Body)
		if err != nil {
			return err
		}
		if err := s.EnsureVectorTable(len(vector)); err != nil {
			return err
		}
		if err := s.SetTraceVector(ctx, trace.ID, vector); err != nil {
			return err
		}
		embedded++
	}
	fmt.Fprintf(stdout, "embedded: %d\n", embedded)
	return nil
}

func runMaintainDecay(args []string, stdout io.Writer) error {
	fs, home, db := commonFlags("maintain decay")
	dryRun := fs.Bool("dry-run", false, "show affected count without updating")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, _, err := openStore(*home, *db)
	if err != nil {
		return err
	}
	defer s.Close()
	count, err := s.Decay(context.Background(), *dryRun)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "decay_candidates: %d\ndry_run: %t\n", count, *dryRun)
	return nil
}

func runMCP(args []string, stdout io.Writer) error {
	fs, home, db := commonFlags("mcp")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, _, err := openStore(*home, *db)
	if err != nil {
		return err
	}
	defer s.Close()
	return mcp.Serve(context.Background(), s, stdout, nil)
}

func runAgent(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hebb agent install --agent codex|claude|all [--apply] | hebb agent hook <mode> | hebb agent instructions")
	}
	switch args[0] {
	case "install":
		return runAgentInstall(args[1:], stdout)
	case "hook":
		return runAgentHook(args[1:], stdout)
	case "instructions":
		return runAgentInstructions(args[1:], stdout)
	default:
		return fmt.Errorf("unknown agent command %q", args[0])
	}
}

func runAgentInstall(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("agent install", flag.ContinueOnError)
	agentName := fs.String("agent", "all", "agent to configure: codex, claude or all")
	apply := fs.Bool("apply", false, "write changes instead of printing the install plan")
	force := fs.Bool("force", false, "reserved for future overwrite behavior")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return agent.Installer{Stdout: stdout}.Install(ctx, agent.InstallOptions{Agent: *agentName, Apply: *apply, Force: *force})
}

func runAgentHook(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: hebb agent hook session-start|user-prompt-submit|stop")
	}
	s, _, err := openStore("", "")
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return agent.HandleHook(ctx, s, args[0], os.Stdin, stdout)
}

func runAgentInstructions(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("agent instructions", flag.ContinueOnError)
	agentName := fs.String("agent", "generic", "agent name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintln(stdout, agent.Instructions(*agentName))
	return nil
}

func parseRelationArgs(args []string) (string, string, string, []string, error) {
	var relation string
	var home string
	var db string
	positional := make([]string, 0, 2)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--home":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("--home requires a value")
			}
			home = args[i+1]
			i++
		case strings.HasPrefix(arg, "--home="):
			home = strings.TrimPrefix(arg, "--home=")
		case arg == "--db":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("--db requires a value")
			}
			db = args[i+1]
			i++
		case strings.HasPrefix(arg, "--db="):
			db = strings.TrimPrefix(arg, "--db=")
		case arg == "--relation":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("--relation requires a value")
			}
			relation = args[i+1]
			i++
		case strings.HasPrefix(arg, "--relation="):
			relation = strings.TrimPrefix(arg, "--relation=")
		default:
			positional = append(positional, arg)
		}
	}
	return relation, home, db, positional, nil
}

func parseCommonArgs(args []string, stringFlags map[string]*string) (string, string, string, []string, error) {
	var home string
	var db string
	values := map[string]string{}
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--home" || arg == "--db" || arg == "--reason":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("%s requires a value", arg)
			}
			switch arg {
			case "--home":
				home = args[i+1]
			case "--db":
				db = args[i+1]
			case "--reason":
				values["reason"] = args[i+1]
			}
			i++
		case strings.HasPrefix(arg, "--home="):
			home = strings.TrimPrefix(arg, "--home=")
		case strings.HasPrefix(arg, "--db="):
			db = strings.TrimPrefix(arg, "--db=")
		case strings.HasPrefix(arg, "--reason="):
			values["reason"] = strings.TrimPrefix(arg, "--reason=")
		default:
			positional = append(positional, arg)
		}
	}
	return home, db, values["reason"], positional, nil
}

func parseForgetArgs(args []string) (string, string, string, []string, map[string]bool, error) {
	home, db, reason, positional, err := parseCommonArgs(args, map[string]*string{"reason": nil})
	if err != nil {
		return "", "", "", nil, nil, err
	}
	bools := map[string]bool{"soft": true}
	filtered := positional[:0]
	for _, arg := range positional {
		switch arg {
		case "--soft":
			bools["soft"] = true
		case "--hard":
			bools["hard"] = true
		case "--yes":
			bools["yes"] = true
		default:
			filtered = append(filtered, arg)
		}
	}
	return home, db, reason, filtered, bools, nil
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid trace id %q", value)
	}
	return id, nil
}

type repeatedFlag []string

func (f *repeatedFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatedFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Hebb is a local-first long-term memory engine for AI agents.

Usage:
  hebb init
  hebb doctor
  hebb encode --kind fact --title "..." --body "..."
  hebb retrieve "query"
  hebb associate <trace-id-a> <trace-id-b> --relation supports
  hebb reinforce <trace-id> --reason "used_in_answer"
  hebb inhibit <trace-id> --reason "stale_or_noisy"
  hebb forget <trace-id> --soft
  hebb consolidate --scope /repo
  hebb inspect trace <trace-id>
  hebb inspect stats
  hebb maintain embed --pending
  hebb maintain decay --dry-run
  hebb mcp
  hebb agent install --agent codex --apply
  hebb agent install --agent claude --apply

Aliases:
  remember -> encode
  recall   -> retrieve
  link     -> associate`)
}
