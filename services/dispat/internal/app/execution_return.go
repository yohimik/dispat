package app

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

const returnAddressMissingEndpoint = ""
const returnAddressMissingListen = ""
const returnAddressNoBind = ""
const returnAddressMissingHost = ""
const returnAddressHTTPScheme = "https"
const returnAddressNoPath = ""
const returnAddressRootPath = "/"
const returnAddressNoQuery = ""
const returnAddressNoFragment = ""
const returnAddressNoOpaque = ""
const returnAddressMinimumPort = 1
const returnAddressMaximumPort = 65535
const returnListenWithoutEndpointError = "--return-listen requires --return-endpoint"
const returnEndpointShapeError = "--return-endpoint requires a credential-free https://host:port address"
const returnListenShapeError = "--return-listen requires host:port"
const returnAddressConfigurationErrorFormat = "return channel configuration is invalid: %w"
const returnHostPortInvalidError = "invalid host or port"
const returnPortInvalidError = "invalid port"

// ValidateReturnAddress resolves the local bind address for a first-node
// return channel. A separate listen address supports NAT without changing
// the signed HTTPS address sent to workers.
func ValidateReturnAddress(endpoint, listen string) (string, error) {
	if endpoint == returnAddressMissingEndpoint {
		if listen != returnAddressMissingListen {
			return returnAddressNoBind, fmt.Errorf(returnListenWithoutEndpointError)
		}
		return returnAddressNoBind, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != returnAddressHTTPScheme || u.User != nil || u.Host == returnAddressMissingHost ||
		u.Path != returnAddressNoPath && u.Path != returnAddressRootPath ||
		u.RawQuery != returnAddressNoQuery || u.Fragment != returnAddressNoFragment ||
		u.Opaque != returnAddressNoOpaque {
		return returnAddressNoBind, fmt.Errorf(returnEndpointShapeError)
	}
	if err := validateReturnHostPort(u.Host); err != nil {
		return returnAddressNoBind, fmt.Errorf(returnEndpointShapeError)
	}
	if listen == returnAddressMissingListen {
		return u.Host, nil
	}
	if err := validateReturnHostPort(listen); err != nil {
		return returnAddressNoBind, fmt.Errorf(returnListenShapeError)
	}
	return listen, nil
}

func (a *App) checkReturnAddress(endpoint, listen string) error {
	if _, err := ValidateReturnAddress(endpoint, listen); err != nil {
		return execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			returnAddressConfigurationErrorFormat, err)
	}
	return nil
}

func validateReturnHostPort(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == returnAddressMissingHost {
		return fmt.Errorf(returnHostPortInvalidError)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < returnAddressMinimumPort || n > returnAddressMaximumPort {
		return fmt.Errorf(returnPortInvalidError)
	}
	return nil
}
