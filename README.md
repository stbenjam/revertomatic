# Revertomatic

Tool for reverting pull requests for TRT.  

## Usage

### Reverts

Ensure you have a GitHub token set.  Get a [personal access
token](https://github.com/settings/tokens) with read:org, repo, and user
privileges:

```
$ export GITHUB_TOKEN="xyzabcdefgh"
```

#### No local clone

If you don't have a local copy of the repository already, you can use the
following command, Revertomatic will create a temporary clone and perform the
revert. For large repositories (origin, kubernetes) this can take a long time
and it's better if you have a local clone already (see next section).

```
./revertomatic \
    -p https://github.com/openshift/kubernetes/pull/1703 \
    -j TRT-9999 \
    -v "Verification steps TBD" \
    -c "This PR broke all jobs on https://amd64.ocp.releases.ci.openshift.org/releasestream/4.15.0-0.nightly/release/4.15.0-0.nightly-2023-10-03-025546"
```

#### Local clone of Repository

To use a local clone, set -l, -u, and -r settings like this:

```
./revertomatic \
    -p https://github.com/openshift/kubernetes/pull/1703 \
    -j TRT-9999 \
    -v "Verification steps TBD" \
    -c "This PR broke all jobs on https://amd64.ocp.releases.ci.openshift.org/releasestream/4.15.0-0.nightly/release/4.15.0-0.nightly-2023-10-03-025546" \
    -l $HOME/go/src/github.com/kubernetes/kubernetes \
    -u origin \
    -r stbenjam
```

### Overrides Only

You can generate the list of overrides for a PR like this:

```
./revertomatic override -p https://github.com/openshift/cluster-network-operator/pull/2037 
```
