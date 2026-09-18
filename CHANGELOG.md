# Changelog

## [0.3.0](https://github.com/kanivet-ai/kanivet-oss/compare/v0.2.0...v0.3.0) (2026-09-18)


### Features

* **frontend:** layered Kanivet mark and regenerated app icons ([fe95db4](https://github.com/kanivet-ai/kanivet-oss/commit/fe95db42209874003bb7b4254a94ce72ee7c4097))
* **frontend:** purpose-built Kubernetes resource icon pack ([6f030ed](https://github.com/kanivet-ai/kanivet-oss/commit/6f030eddc38163f51e24463f69a8b6b66634b770))
* **frontend:** refresh resource icon pack and Kanivet mark ([3996cef](https://github.com/kanivet-ai/kanivet-oss/commit/3996ceff76aea5eb660b9c308211537cff6eef1b))


### Bug Fixes

* bounded startup index load, DB retention, vcluster health recovery ([e7d9ed8](https://github.com/kanivet-ai/kanivet-oss/commit/e7d9ed801b9d5ca6d40fddda249c8557a744b887))
* consistent kind and resource naming in search, single sidebar selection, detail pane styling ([f434c63](https://github.com/kanivet-ai/kanivet-oss/commit/f434c63a5017f19a3be0b902690559a9c0c7e8b5))
* **db:** purge search rows written before kind normalization ([0b4ebc9](https://github.com/kanivet-ai/kanivet-oss/commit/0b4ebc9829494362f857250ce18d09429e7e9411))
* **db:** version the schema, purge stale and orphaned rows, reclaim space ([1122727](https://github.com/kanivet-ai/kanivet-oss/commit/1122727db947f832d68a4710d8a60a54e77c2875))
* **edit:** never recreate a deleted resource when saving from the YAML editor ([78e0a8f](https://github.com/kanivet-ai/kanivet-oss/commit/78e0a8fda4ed7685f0fc9aee8e2febd9b724047d))
* **events:** scope resource events by kind, not just name and namespace ([2db4412](https://github.com/kanivet-ai/kanivet-oss/commit/2db441269d4bb6246043c8cab1792eadf40d35b6))
* **frontend:** give discovered kinds a fitting icon instead of a document ([e18ca88](https://github.com/kanivet-ai/kanivet-oss/commit/e18ca881be9d52991f53df4ff656da32ada3d372))
* **frontend:** open search results through the tree's resource node ([e883448](https://github.com/kanivet-ai/kanivet-oss/commit/e8834482f32b3fff48e1a82f50c355a4881cfd4b))
* **frontend:** preserve conflict protection when saving YAML ([4fe7ad8](https://github.com/kanivet-ai/kanivet-oss/commit/4fe7ad838e8ef4c1956c3d021124c20a83213ab0))
* **frontend:** preserve conflict protection when saving YAML ([af33937](https://github.com/kanivet-ai/kanivet-oss/commit/af33937c42c0ed7b09a9c25b088de42430934e05))
* **frontend:** report failed resource actions accurately ([54a3af2](https://github.com/kanivet-ai/kanivet-oss/commit/54a3af27baeaf7da23ed36b0d7fed1f1ef9a25ff))
* **frontend:** report failed resource actions accurately ([65ad270](https://github.com/kanivet-ai/kanivet-oss/commit/65ad2701231863dec6d22efe7e7e9c2399765254))
* **frontend:** resume log streams after reconnecting ([1ac561d](https://github.com/kanivet-ai/kanivet-oss/commit/1ac561df026dc8da6f81de8d51ef074c55924e18))
* **frontend:** resume log streams after reconnecting ([aa7bb88](https://github.com/kanivet-ai/kanivet-oss/commit/aa7bb88110dcf9aa4c8ac0d7edaec054c7f4ebea))
* **metrics:** show Mimir discovery failures instead of an empty list ([430b67e](https://github.com/kanivet-ai/kanivet-oss/commit/430b67ea23eacbea19161be402f72336023f8448))
* **search:** index one document per object, with the discovery kind and resource name ([b5ff5f7](https://github.com/kanivet-ai/kanivet-oss/commit/b5ff5f7c3e5c2c77e3b95d874296ea51d4775406))
* **search:** search every selected cluster instead of only the first ([37d9381](https://github.com/kanivet-ai/kanivet-oss/commit/37d93815e0ffe052dfafa852fd760d1a232193ef))
* **sidebar:** drop the kanivetIDE root node and highlight a single selected row ([e813c9a](https://github.com/kanivet-ai/kanivet-oss/commit/e813c9acfd02ce75b729abb44b2bfe7c6c4eee9f))
* six behaviour fixes — terminal reconnect, kind-scoped events, action error toasts, no recreate on edit, multi-cluster search, Mimir discovery errors ([a59cc01](https://github.com/kanivet-ai/kanivet-oss/commit/a59cc0126af3e160a117613a2fe36bbf5f29f99c))
* **terminal:** recreate the shell session after a WebSocket reconnect ([a2a03c3](https://github.com/kanivet-ai/kanivet-oss/commit/a2a03c3fba5627e4dab6131592953ea6e181220e))
* **ui:** surface failed operational actions as error toasts ([03d25f3](https://github.com/kanivet-ai/kanivet-oss/commit/03d25f3546c8e28986270c00a74ce692362951a8))
* **vcluster:** return the supervisor to healthy after a transient probe failure ([dd25b7f](https://github.com/kanivet-ai/kanivet-oss/commit/dd25b7f4484302c74c70ca910cc3b40da17bbc08))


### Performance

* **search:** load the persisted index once, into shards, within a budget ([bcbf17b](https://github.com/kanivet-ai/kanivet-oss/commit/bcbf17bcdd79ed514ed883ff295e739d16d93787))
* **websocket:** assemble batch envelopes without re-encoding raw events ([1301edb](https://github.com/kanivet-ai/kanivet-oss/commit/1301edb40165ed6c68b6195d803b78920f0121df))


### Documentation

* **readme:** center the new Kanivet logo above the title ([5763a7c](https://github.com/kanivet-ai/kanivet-oss/commit/5763a7c24ebb3908b9ef3a1382ac0275eb4cd9ad))
* **readme:** improve Kubernetes feature discovery and getting started ([f1d6a27](https://github.com/kanivet-ai/kanivet-oss/commit/f1d6a273b3cb910f3106d01cbbed60f39814d56f))


### Maintenance

* Upgrade backend to latest Go version (1.27), replace json library with recent builtin JSON v2, added optional devbox ([#6](https://github.com/kanivet-ai/kanivet-oss/issues/6)) ([e0164a2](https://github.com/kanivet-ai/kanivet-oss/commit/e0164a2f2132fe891efddcfa723340525db686a4))

## [0.2.0](https://github.com/kanivet-ai/kanivet-oss/compare/v0.1.2...v0.2.0) (2026-09-11)


### Features

* show CRD printer columns on custom resource lists ([9d3212c](https://github.com/kanivet-ai/kanivet-oss/commit/9d3212c8372fe148c4210a0d9c95bd2e7e410a36))
* show CRD printer columns on custom resource lists ([03b572e](https://github.com/kanivet-ai/kanivet-oss/commit/03b572e8e16da4869b5ff4469e696b478348962c))

## [0.1.2](https://github.com/kanivet-ai/kanivet-oss/compare/v0.1.1...v0.1.2) (2026-09-10)


### Documentation

* add project governance and adopt Apache-2.0 ([#4](https://github.com/kanivet-ai/kanivet-oss/issues/4)) ([2ec6cb8](https://github.com/kanivet-ai/kanivet-oss/commit/2ec6cb8c7e4476667e58814de3a9fb24a9b6270f))

## [0.1.1](https://github.com/kanivet-ai/kanivet-oss/compare/v0.1.0...v0.1.1) (2026-09-08)


### Bug Fixes

* enable automated releases and resolve platform failures ([#2](https://github.com/kanivet-ai/kanivet-oss/issues/2)) ([4bcea66](https://github.com/kanivet-ai/kanivet-oss/commit/4bcea669a47a2a6209379d2833f120601d855a30))


### Performance

* port upstream PR [#121](https://github.com/kanivet-ai/kanivet-oss/issues/121) efficiency improvements ([33bf576](https://github.com/kanivet-ai/kanivet-oss/commit/33bf5760ec3af6626b92f017ab575121d459b8de))
* reduce idle CPU, network and re-render churn ([3d7681f](https://github.com/kanivet-ai/kanivet-oss/commit/3d7681fd56112038d043e3f81b0ef5fd94c6c1ed))


### Maintenance

* create clean Kanivet source export ([179a87c](https://github.com/kanivet-ai/kanivet-oss/commit/179a87cb1c92add10599788c6a2f7d3384c1ae6e))
