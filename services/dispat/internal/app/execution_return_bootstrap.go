package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

const returnHandlerCannotBindBrokerError = "execution: return channel cannot bind task broker"
const returnHandlerTempDirectoryParent = ""
const returnHandlerTempDirectoryPrefix = "dispat-return-"
const returnHandlerUnsetTempDirectory = ""
const returnHandlerChoreographyRoute = "/dispat/choreography/v1/"
const retainedReturnAddressInvalidErrorFormat = "execution: retained return address is invalid: %w"
const retainedReturnAddressIdentityMismatchError = "execution: retained return address differs from protected run identity"
const retainedReturnCertificateInvalidErrorFormat = "execution: retained return certificate cannot serve: %w"
const retainedReturnListenerBindErrorFormat = "execution: retained return listener cannot bind: %w"
const returnProbeMissingPreflightError = "execution: return channel or coordinator is absent before preflight"
const returnEndpointRequiredError = "distributed execution requires --return-endpoint for worker replies"
const returnEndpointInvalidErrorFormat = "return channel configuration is invalid: %w"
const returnJournalMissingError = "execution: first-node return identity was not prepared before remote effects"
const returnEndpointPreparedIdentityMismatchError = "execution: return endpoint differs from the prepared run identity"
const returnPreparedCertificateReadErrorFormat = "execution: reading prepared return channel identity: %w"
const returnListenerBindErrorFormat = "return channel could not listen: %w"
const returnChannelCloseTimeout = 5 * time.Second
const returnChannelUnspecifiedEndpoint = ""

type runReturnChannel struct {
	endpoint string
	certPin  string
	gate     *runReturnGate
	probe    http.Handler
	tempDir  string
	server   *runReturnServer
}

type taskReturnHandlerBinding struct {
	binding execution.ChoreographyRunBinding
	signer  *execution.Signer
	limits  execution.TransferLimits
	broker  execution.ChoreographyReturnBrokerx
	channel *runReturnChannel
}

type retainedRunReturnChannelOptions struct {
	identity firstRunIdentity
	signer   *execution.Signer
	limits   execution.TransferLimits
	binding  execution.ChoreographyRunBinding
	broker   execution.ChoreographyReturnBrokerx
}

// bindTaskReturnHandler enables claims only after every canonical task and
// ledger ref has been read back and the broker has indexed the exact graph.
// The probe route remains live for the same signed run identity.
func (a *App) bindTaskReturnHandler(coordinator *execution.Coordinator,
	broker execution.ChoreographyReturnBrokerx, channel *runReturnChannel) error {
	if coordinator == nil || channel == nil || channel.probe == nil {
		return fmt.Errorf(returnHandlerCannotBindBrokerError)
	}
	return bindTaskReturnHandler(taskReturnHandlerBinding{binding: execution.ChoreographyRunBinding{Run: coordinator.Run,
		Generation: coordinator.Generation, PlanDigest: coordinator.PlanDigest,
		GraphDigest: coordinator.GraphDigest}, signer: coordinator.Signer, limits: coordinator.Limits,
		broker: broker, channel: channel})
}

func bindTaskReturnHandler(options taskReturnHandlerBinding) error {
	binding, signer, limits, broker, channel := options.binding, options.signer,
		options.limits, options.broker, options.channel
	if channel == nil || channel.gate == nil || broker == nil || channel.tempDir != returnHandlerUnsetTempDirectory {
		return fmt.Errorf(returnHandlerCannotBindBrokerError)
	}
	tempDir, err := os.MkdirTemp(returnHandlerTempDirectoryParent, returnHandlerTempDirectoryPrefix)
	if err != nil {
		return err
	}
	handler, err := execution.NewChoreographyReturnHandler(execution.ReturnHandlerOptions{
		Broker: broker, Signer: signer, Binding: binding,
		MaxClaimBytes:  int64(limits.MaxManifestBytes),
		MaxResultBytes: limits.MaxPackBytes,
		TempDir:        tempDir,
	})
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return err
	}
	mux := http.NewServeMux()
	if channel.probe != nil {
		mux.Handle(execution.ChoreographyProbePath, channel.probe)
	}
	mux.Handle(returnHandlerChoreographyRoute, handler)
	channel.tempDir = tempDir
	channel.gate.bind(mux)
	return nil
}

// openRetainedRunReturnChannel restores the exact protected endpoint identity
// of an interrupted generation. Its task handler remains gated until the
// canonical bootstrap, ledger and broker have been authenticated and replayed.
func openRetainedRunReturnChannel(options retainedRunReturnChannelOptions) (*runReturnChannel, error) {
	identity, signer, limits, binding, broker := options.identity, options.signer,
		options.limits, options.binding, options.broker
	bind, err := ValidateReturnAddress(identity.Endpoint, identity.Listen)
	if err != nil {
		return nil, fmt.Errorf(retainedReturnAddressInvalidErrorFormat, err)
	}
	if bind != identity.Listen || identity.Run != binding.Run ||
		identity.Generation != binding.Generation {
		return nil, fmt.Errorf(retainedReturnAddressIdentityMismatchError)
	}
	cert, err := identity.certificate()
	if err != nil {
		return nil, fmt.Errorf(retainedReturnCertificateInvalidErrorFormat, err)
	}
	channel := &runReturnChannel{endpoint: identity.Endpoint, certPin: identity.CertPin,
		gate: &runReturnGate{}}
	channel.server, err = startRunReturnServer(bind, cert, channel.gate)
	if err != nil {
		return nil, fmt.Errorf(retainedReturnListenerBindErrorFormat, err)
	}
	if err := bindTaskReturnHandler(taskReturnHandlerBinding{binding: binding, signer: signer,
		limits: limits, broker: broker, channel: channel}); err != nil {
		_ = channel.close()
		return nil, err
	}
	return channel, nil
}

func (a *App) bindProbeReturnHandler(coordinator *execution.Coordinator, channel *runReturnChannel) error {
	if channel == nil || coordinator == nil {
		return fmt.Errorf(returnProbeMissingPreflightError)
	}
	handler, err := execution.NewChoreographyProbeHandler(execution.ProbeHandlerOptions{
		Coordinator: coordinator, Signer: coordinator.Signer,
		Binding: execution.ChoreographyRunBinding{Run: coordinator.Run,
			Generation: coordinator.Generation, PlanDigest: coordinator.PlanDigest,
			GraphDigest: coordinator.GraphDigest},
		MaxBytes: int64(coordinator.Limits.MaxManifestBytes),
	})
	if err != nil {
		return err
	}
	channel.probe = handler
	channel.gate.bind(handler)
	return nil
}

// openRunReturnChannel binds before the signed preflight advertises the
// endpoint. A bound but uninitialized broker gives a bounded refusal.
func (a *App) openRunReturnChannel(endpoint, listen string) (*runReturnChannel, error) {
	if !a.cfg.Execution.IsDistributed() {
		return nil, nil
	}
	if endpoint == returnChannelUnspecifiedEndpoint {
		return nil, execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			returnEndpointRequiredError)
	}
	bind, err := ValidateReturnAddress(endpoint, listen)
	if err != nil {
		return nil, execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			returnEndpointInvalidErrorFormat, err)
	}
	if a.returnJournal == nil {
		return nil, fmt.Errorf(returnJournalMissingError)
	}
	identity, err := a.returnJournal.read()
	if err != nil {
		return nil, err
	}
	if identity.Endpoint != endpoint || identity.Listen != bind || identity.Run != a.runID {
		return nil, fmt.Errorf(returnEndpointPreparedIdentityMismatchError)
	}
	cert, err := identity.certificate()
	if err != nil {
		return nil, fmt.Errorf(returnPreparedCertificateReadErrorFormat, err)
	}
	gate := &runReturnGate{}
	server, err := startRunReturnServer(bind, cert, gate)
	if err != nil {
		return nil, execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			returnListenerBindErrorFormat, err)
	}
	return &runReturnChannel{endpoint: endpoint, certPin: identity.CertPin, gate: gate, server: server}, nil
}

func (c *runReturnChannel) close() error {
	if c == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), returnChannelCloseTimeout)
	defer cancel()
	err := c.server.close(ctx)
	if c.tempDir != returnHandlerUnsetTempDirectory {
		err = errors.Join(err, os.RemoveAll(c.tempDir))
	}
	return err
}
