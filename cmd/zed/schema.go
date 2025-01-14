package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/TylerBrock/colorjson"
	"github.com/authzed/authzed-go/proto/authzed/api/v1alpha1"
	authzedv1alpha1 "github.com/authzed/authzed-go/v1alpha1"
	"github.com/jzelinskie/cobrautil"
	"github.com/jzelinskie/stringz"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/authzed/zed/internal/storage"
)

func registerSchemaCmd(rootCmd *cobra.Command) {
	rootCmd.AddCommand(schemaCmd)

	schemaCmd.AddCommand(schemaReadCmd)
	schemaReadCmd.Flags().Bool("json", false, "output as JSON")

	schemaCmd.AddCommand(schemaWriteCmd)
	schemaWriteCmd.Flags().Bool("json", false, "output as JSON")
}

var schemaCmd = &cobra.Command{
	Use:               "schema <subcommand>",
	Short:             "read and write to a Schema for a Permissions System",
	PersistentPreRunE: persistentPreRunE,
}

var schemaReadCmd = &cobra.Command{
	Use:               "read <object definitions...>",
	Args:              cobra.MinimumNArgs(1),
	Short:             "read the Schema of current Permissions System",
	PersistentPreRunE: persistentPreRunE,
	RunE:              schemaReadCmdFunc,
}

var schemaWriteCmd = &cobra.Command{
	Use:               "write <file?>",
	Args:              cobra.MaximumNArgs(1),
	Short:             "write a Schema file (or stdin) to the current Permissions System",
	PersistentPreRunE: persistentPreRunE,
	RunE:              schemaWriteCmdFunc,
}

// TODO(jzelinskie): eventually make a variant that takes 0 args and returns
// all object definitions in the schema.
func schemaReadCmdFunc(cmd *cobra.Command, args []string) error {
	token, err := storage.DefaultToken(
		cobrautil.MustGetString(cmd, "permissions-system"),
		cobrautil.MustGetString(cmd, "endpoint"),
		cobrautil.MustGetString(cmd, "token"),
	)
	if err != nil {
		return err
	}
	log.Trace().Interface("token", token).Send()

	client, err := authzedv1alpha1.NewClient(token.Endpoint, dialOptsFromFlags(cmd, token.Secret)...)
	if err != nil {
		return err
	}

	var objDefs []string
	for _, arg := range args {
		if !strings.Contains(arg, "/") {
			arg = stringz.Join("/", token.System, arg)
		}
		objDefs = append(objDefs, arg)
	}

	request := &v1alpha1.ReadSchemaRequest{ObjectDefinitionsNames: objDefs}
	log.Trace().Interface("request", request).Msg("requesting schema read")

	resp, err := client.ReadSchema(context.Background(), request)
	if err != nil {
		return err
	}

	if cobrautil.MustGetBool(cmd, "json") || !term.IsTerminal(int(os.Stdout.Fd())) {
		prettyProto, err := prettyProto(resp)
		if err != nil {
			return err
		}

		fmt.Println(string(prettyProto))
		return nil
	}

	fmt.Println(stringz.Join("\n\n", resp.ObjectDefinitions...))
	return nil
}

func schemaWriteCmdFunc(cmd *cobra.Command, args []string) error {
	token, err := storage.DefaultToken(
		cobrautil.MustGetString(cmd, "permissions-system"),
		cobrautil.MustGetString(cmd, "endpoint"),
		cobrautil.MustGetString(cmd, "token"),
	)
	if err != nil {
		return err
	}
	log.Trace().Interface("token", token).Send()

	client, err := authzedv1alpha1.NewClient(token.Endpoint, dialOptsFromFlags(cmd, token.Secret)...)
	if err != nil {
		return err
	}

	var schemaBytes []byte
	switch len(args) {
	case 1:
		schemaBytes, err = os.ReadFile(args[0])
		log.Trace().Str("schema", string(schemaBytes)).Str("file", args[0]).Msg("read schema from file")
	case 0:
		schemaBytes, err = ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		log.Trace().Str("schema", string(schemaBytes)).Msg("read schema from stdin")
	default:
		panic("schemaWriteCmdFunc called with incorrect number of arguments")
	}

	if len(schemaBytes) == 0 {
		log.Fatal().Msg("attempted to write empty schema")
	}

	request := &v1alpha1.WriteSchemaRequest{
		Schema: string(schemaBytes),
	}
	log.Trace().Interface("request", request).Msg("writing schema")

	resp, err := client.WriteSchema(context.Background(), request)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to write schema")
	}
	log.Trace().Interface("response", resp).Msg("wrote schema")

	if cobrautil.MustGetBool(cmd, "json") || !term.IsTerminal(int(os.Stdout.Fd())) {
		prettyProto, err := prettyProto(resp)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to convert schema to JSON")
		}

		fmt.Println(string(prettyProto))
		return nil
	}

	fmt.Printf("%s\n", stringz.Join("\n", resp.GetObjectDefinitionsNames()...))
	return nil
}

func prettyProto(m proto.Message) ([]byte, error) {
	encoded, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var obj interface{}
	err = json.Unmarshal(encoded, &obj)
	if err != nil {
		panic("protojson decode failed: " + err.Error())
	}

	f := colorjson.NewFormatter()
	f.Indent = 2
	pretty, err := f.Marshal(obj)
	if err != nil {
		panic("colorjson encode failed: " + err.Error())
	}

	return pretty, nil
}
