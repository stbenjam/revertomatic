# Revertomatic

Tool for reverting pull requests for TRT.  At the moment it doesn't do much
other than spit out the override commands needed, which is a bit of a pain
otherwise since you can't easily copy and paste from the GitHub status.
Eventually it'll try to do as much of the TRT revert workflow as possible,
including Jira stuff.

## Usage

Get a GitHub token and then do this:
    
```
$ export GITHUB_TOKEN="xyzabcdefgh"
$ make
$ ./revertomatic -p https://github.com/openshift/origin/pull/28242 -o

INFO[0000] found info for pr https://github.com/openshift/origin/pull/28242  owner=openshift pr_number=28242 repo=origin
INFO[0000] Most recent SHA of the PR: 5f853f06192eaff21edc6ade16a77d1e85ba149e 
INFO[0000] comment this to override ci contexts and force your PR 
/override ci/prow/e2e-agnostic-ovn-cmd
/override ci/prow/e2e-metal-ipi-sdn
/override ci/prow/e2e-metal-ipi-ovn-ipv6
/override ci/prow/e2e-gcp-ovn
/override ci/prow/e2e-openstack-ovn
/override ci/prow/e2e-gcp-csi
/override ci/prow/e2e-aws-ovn-single-node-upgrade
/override ci/prow/e2e-aws-ovn-serial
/override ci/prow/e2e-aws-ovn-cgroupsv2
/override ci/prow/e2e-aws-csi
/override ci/prow/e2e-gcp-ovn-rt-upgrade
/override ci/prow/e2e-gcp-ovn-upgrade
/override ci/prow/e2e-aws-ovn-upgrade
/override ci/prow/e2e-aws-ovn-single-node
/override ci/prow/e2e-aws-ovn-single-node-serial
/override ci/prow/e2e-aws-ovn-fips
```
