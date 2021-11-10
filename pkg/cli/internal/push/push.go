package push

import (
	"context"
	"fmt"

	"github.com/pterm/pterm"
	"github.com/solo-io/gloobpf/pkg/cli/internal/options"
	"github.com/solo-io/gloobpf/pkg/packaging"
	"github.com/spf13/cobra"
	"oras.land/oras-go/pkg/content"
	"oras.land/oras-go/pkg/oras"
)

type pushOptions struct {
	general *options.GeneralOptions
}

func Command(opts *options.GeneralOptions) *cobra.Command {
	pushOpts := &pushOptions{
		general: opts,
	}
	cmd := &cobra.Command{
		Use:  "push",
		Args: cobra.ExactArgs(1), // Ref
		RunE: func(cmd *cobra.Command, args []string) error {
			return push(cmd.Context(), pushOpts.general, args[0])
		},
	}

	return cmd
}

func push(ctx context.Context, opts *options.GeneralOptions, ref string) error {
	pterm.Info.Printfln("Pushing eBPF image %s", ref)

	localRegistry, err := content.NewOCI(opts.OCIStorageDir)
	if err != nil {
		return err
	}

	remoteRegistry, err := content.NewRegistry(opts.AuthOptions.ToRegistryOptions())
	if err != nil {
		return err
	}

	pushSpinner, _ := pterm.DefaultSpinner.Start("Pushing image %s to remote registry", ref)
	_, err = oras.Copy(
		ctx,
		localRegistry,
		ref,
		remoteRegistry,
		"",
		oras.WithAllowedMediaTypes(packaging.AllowedMediaTypes()),
	)
	if err != nil {
		pushSpinner.UpdateText(fmt.Sprintf("Failed to push image %s", ref))
		pushSpinner.Fail()
		return err
	}
	pushSpinner.Success()
	return nil

}
