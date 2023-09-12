// Copyright 2023, Pulumi Corporation.  All rights reserved.

package ast

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/pulumi/environments/syntax/encoding"
)

const example = `
imports:
  - green-channel
  - us-west-2
config:
  aws:
    fn::open:
      provider: aws-oidc
      inputs:
        sessionName: site-prod-session
        roleArn: some-role-arn
  pulumi:
    aws:defaultTags:
      tags:
        environment: prod
`

func TestExample(t *testing.T) {
	t.Parallel()

	syntax, diags := encoding.DecodeYAML("<stdin>", yaml.NewDecoder(strings.NewReader(example)), nil)
	require.Len(t, diags, 0)

	environment, diags := ParseEnvironment([]byte(example), syntax)
	assert.Len(t, diags, 0)

	assert.Nil(t, environment.Description)
}
