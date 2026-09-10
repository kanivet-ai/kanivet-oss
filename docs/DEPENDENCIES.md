# Dependency and asset licensing

The project code license does not determine dependency licenses. Review Go modules, npm packages, Electron/Chromium distributions, copied source, themes, fonts, and images separately.

## Inputs

- [backend/go.mod](../backend/go.mod) and [backend/go.sum](../backend/go.sum) identify Go modules.
- [frontend/package-lock.json](../frontend/package-lock.json) identifies frontend, Electron, and packaging dependencies.
- [package-lock.json](../package-lock.json) identifies root tooling dependencies.
- `frontend/assets`, application resources, and copied source need provenance checks beyond package metadata.

## Review procedure

1. Resolve the locked dependency trees in a clean environment. Inventory exact versions, upstream sources, license expressions, and required notices. Include transitive and runtime-bundled components, and distinguish development-only tools.
2. Check actual upstream license files. Missing or ambiguous package metadata requires investigation, not an assumption of MIT or Apache-2.0.
3. Compare component use with the [CNCF third-party license policy](https://github.com/cncf/foundation/blob/main/policies-guidance/allowed-third-party-license-policy.md). Its blanket exception conditions include more than the license name. Check [approved exceptions](https://exceptions.cncf.io/) where necessary.
4. Replace incompatible components, change how they are incorporated where permitted, or seek an exception through the CNCF process. Record component, version, reason, and decision in the relevant review.
5. Include required notices in both source and packaged distributions. Generate an SBOM and third-party attribution report for the exact release artifacts; inspect the package to verify they are present.
6. Run vulnerability analysis separately. A license review does not establish that a dependency is secure.

Useful inventory starting points after installing dependencies:

```bash
cd backend
go list -m -json all > /tmp/kanivet-go-modules.json
cd ../frontend
npm ls --all --json > /tmp/kanivet-npm-tree.json
```

These commands inventory modules; they do not themselves perform a full license audit. Choose and configure a license/SBOM scanner suitable for Go, npm, and the packaged Electron runtime, then manually review exceptions and non-package assets.
