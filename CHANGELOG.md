## [1.2.3](https://github.com/denyzzko/nixpkgs-notifier/compare/v1.2.2...v1.2.3) (2026-04-16)


### Bug Fixes

* add CSRF protection via nosurf ([18c06ff](https://github.com/denyzzko/nixpkgs-notifier/commit/18c06ff39a9cbaea3f778b1fd666272655f14ea2))

## [1.2.2](https://github.com/denyzzko/nixpkgs-notifier/compare/v1.2.1...v1.2.2) (2026-04-16)


### Bug Fixes

* corrected CSP to allow jsdelivr and simpleicons resources ([d3bc2ea](https://github.com/denyzzko/nixpkgs-notifier/commit/d3bc2ea45f7d4f334f5c51f5a4ec55b985402f3f))

## [1.2.1](https://github.com/denyzzko/nixpkgs-notifier/compare/v1.2.0...v1.2.1) (2026-04-16)


### Bug Fixes

* add security headers and TRUST_PROXY config ([aac9a90](https://github.com/denyzzko/nixpkgs-notifier/commit/aac9a90fd0db6ca20a893db054c1f25f9bcc0269))

# [1.2.0](https://github.com/denyzzko/nixpkgs-notifier/compare/v1.1.0...v1.2.0) (2026-04-16)


### Features

* added automatic notification cleanup configurable via admin config page ([a736e8e](https://github.com/denyzzko/nixpkgs-notifier/commit/a736e8e98f3636f817fab3dca05337a0efca05fd))

# [1.1.0](https://github.com/denyzzko/nixpkgs-notifier/compare/v1.0.0...v1.1.0) (2026-04-12)


### Features

* account linking ([7f296fc](https://github.com/denyzzko/nixpkgs-notifier/commit/7f296fcaa5b3852e82b31f264acf5f36b8e55fbc))

# 1.0.0 (2026-04-07)


### Bug Fixes

* added missing Date header ([cc8fe95](https://github.com/denyzzko/nixpkgs-notifier/commit/cc8fe956e4d23c36d9049aa97d039250da80b661))
* address CI workflow permissions and simplify test step ([e22c059](https://github.com/denyzzko/nixpkgs-notifier/commit/e22c059dfd7a696be28803153541830ea5c8c206))
* adjust navbar based on login status and login page to home page redirect when logged in ([6c6fa43](https://github.com/denyzzko/nixpkgs-notifier/commit/6c6fa43dd3cc6aa3ea09b367c16c907ef752691e))
* better handling for wrong package name or branch entered for Track operation ([af5c6e0](https://github.com/denyzzko/nixpkgs-notifier/commit/af5c6e092e6c54df8cf259e00583c4415438220f))
* bugs in previous db changes ([1a9a920](https://github.com/denyzzko/nixpkgs-notifier/commit/1a9a92081d7fbe635bb96083263c5449540980a4))
* change home page internal naming to index ([b578a47](https://github.com/denyzzko/nixpkgs-notifier/commit/b578a47c27a75b2110d6d7ce669844a58e34ceae))
* database schema and operations based on new design ([c020567](https://github.com/denyzzko/nixpkgs-notifier/commit/c020567073df952093708763a5522999f5875ac4))
* full ssl/tls support ([6257ef8](https://github.com/denyzzko/nixpkgs-notifier/commit/6257ef88c6c101ba1cd40ab1a6a155ce64afa988))
* install semantic-release plugins before release ([f98e5b7](https://github.com/denyzzko/nixpkgs-notifier/commit/f98e5b759e7d84a1091669fd742b84057c8e1f27))
* logout button and unauthenticated SMTP connections not working ([2a1fa22](https://github.com/denyzzko/nixpkgs-notifier/commit/2a1fa22f82c507d5f48e4cfc3a88eb276a9891e4))
* made check and track operations run nix eval async for each package (and some further refactoring) ([8f9529f](https://github.com/denyzzko/nixpkgs-notifier/commit/8f9529f31d802a10236277c1f699777455d79a73))
* **nixos-module:** make postgres password hook idempotent ([06d1572](https://github.com/denyzzko/nixpkgs-notifier/commit/06d157291fc5731c927d349e3d7a91eb3b7d420d))
* **nixos-module:** use perSystem package default ([40b4ac9](https://github.com/denyzzko/nixpkgs-notifier/commit/40b4ac97d7c9ab64538d70c406ec17ac387ddaf5))
* **nixos:** constrain postgresql user/name to safe identifier pattern and add double-quote escaping ([0fba1ce](https://github.com/denyzzko/nixpkgs-notifier/commit/0fba1cede3bb7b7d1b2150fdedd8270c9e8d5792))
* **nixos:** create PostgreSQL dataDir via systemd-tmpfiles ([1efbab5](https://github.com/denyzzko/nixpkgs-notifier/commit/1efbab55e7f4187add8ffcdd05199618d927851a))
* **nixos:** use psqlSchema for dataDir default instead of manual version extraction ([36271ce](https://github.com/denyzzko/nixpkgs-notifier/commit/36271cedf78aff81f2d344091a4e6119932a3cf4))
* **nixos:** use writable home for nixpkgs-notifier service user ([a35806c](https://github.com/denyzzko/nixpkgs-notifier/commit/a35806cfb5fc740060b5d7229793e47f9a8b88b5))
* refactoring - mainly extracting from endpoint handling (router.go) business logic (app/) and auth logic (auth/) ([79e3372](https://github.com/denyzzko/nixpkgs-notifier/commit/79e3372040a377fd6e42ccb990f435aa2c7ccd22))
* wrap functions with goose statement markers to fix broken body problem ([898039f](https://github.com/denyzzko/nixpkgs-notifier/commit/898039fa0b0567fcd97456cc13d499d153ac70a9))


### Features

* added db migration on server start (runs sql for creating tables if they dont exist yet) ([17d0ce8](https://github.com/denyzzko/nixpkgs-notifier/commit/17d0ce86e182188fcbd3225b5945e7a3d51a8e66))
* added middleware and nix packages ([a9e6ec0](https://github.com/denyzzko/nixpkgs-notifier/commit/a9e6ec077f2dc7c06068c00efa93f0b7a525c421))
* added README ([e9a5648](https://github.com/denyzzko/nixpkgs-notifier/commit/e9a5648e293841bd27fe5ff5c9e7db81b27646bd))
* added skip interval for checker and overall refactoring ([bb9d831](https://github.com/denyzzko/nixpkgs-notifier/commit/bb9d831c3e47592cb1fe4ec4ef87ac282b179283))
* admin config from UI ([933de76](https://github.com/denyzzko/nixpkgs-notifier/commit/933de762567b4caa80207a95aee366614fba92cd))
* background branch fetcher for dropdown list when choosing branch for new package ([40ad4f0](https://github.com/denyzzko/nixpkgs-notifier/commit/40ad4f0bad0beee55ce209f804a81759d865a5b6))
* background checker for periodic checks and high/low priority nix eval queues ([a76f75f](https://github.com/denyzzko/nixpkgs-notifier/commit/a76f75fa3786bbdf8c42ce5291f366abea113495))
* basic UI for basic package tracking operations (and some refactoring) ([2a1c18a](https://github.com/denyzzko/nixpkgs-notifier/commit/2a1c18a435a206bd195f8f15a14a6a446388ef29))
* configuration from a .env file ([9e9cde5](https://github.com/denyzzko/nixpkgs-notifier/commit/9e9cde52b4124da2bb9e3fb40230cd084a9820f7))
* connection to postgres database (and some project structure refactoring) ([942b88e](https://github.com/denyzzko/nixpkgs-notifier/commit/942b88eb11c8e7a683c1fba19fe3e65d49871c65))
* db migrations using goose library ([9e62aca](https://github.com/denyzzko/nixpkgs-notifier/commit/9e62acaf4dbceebd7e29259c411a6dea05db07e2))
* **dev-container:** improve lifecycle commands and remove ssh mode ([021aac9](https://github.com/denyzzko/nixpkgs-notifier/commit/021aac909bc67f62c366107391ac44860a8760a3))
* **dev-container:** provision postgres and stable oidc bootstrap ([58e53b0](https://github.com/denyzzko/nixpkgs-notifier/commit/58e53b0d0254471dad1198d683abb5e6e1202625))
* **dev-container:** support local untracked oidc config ([eb588aa](https://github.com/denyzzko/nixpkgs-notifier/commit/eb588aa8cb512455a1a697ab9da5f6bc551a581b))
* **email:** add SMTP email configuration to NixOS module ([fd60dc6](https://github.com/denyzzko/nixpkgs-notifier/commit/fd60dc6b520b292b4d8176aae5083fbf9884ed12))
* error handling refactoring with new appError package ([f03f663](https://github.com/denyzzko/nixpkgs-notifier/commit/f03f663e0f48a6a5657a03530ec84db62effc410))
* helloworld api server ([0172083](https://github.com/denyzzko/nixpkgs-notifier/commit/01720838d2c053581d6c087d8f22d6d5b1bb245f))
* highlight channel when it was deactivated by server ([ceb9ce3](https://github.com/denyzzko/nixpkgs-notifier/commit/ceb9ce3fa7ffb37fdf50a31ffa214247ce1c56bc))
* identity provider setup for OIDC ([11c3f11](https://github.com/denyzzko/nixpkgs-notifier/commit/11c3f118647125bc1d700612f5327cc084b187a9))
* insert new tracking into database and refactoring ([5da6934](https://github.com/denyzzko/nixpkgs-notifier/commit/5da6934f9dbfd795b5e8e5304b7cb099210464d4))
* new env package to handle environment variables ([e2918fa](https://github.com/denyzzko/nixpkgs-notifier/commit/e2918fa41e13f5308ead70747c02b78f22756720))
* nix version evaluation based on specific branch ([0e6998e](https://github.com/denyzzko/nixpkgs-notifier/commit/0e6998e2a89d976810c960b2a3bd8ae1f79a81d1))
* **nix:** bootstrap flake-based development setup ([b7ce52d](https://github.com/denyzzko/nixpkgs-notifier/commit/b7ce52de2236c46e6f132d003c53e4e7068785ca))
* **nixos:** add PostgreSQL package option, default to postgresql_18 ([f232538](https://github.com/denyzzko/nixpkgs-notifier/commit/f2325388ce6cf40bb9404470698168ea69fbd7cf))
* **nixos:** add SMTP module config, dynamic OIDC callback URL, and fix nix cache permissions ([#1](https://github.com/denyzzko/nixpkgs-notifier/issues/1)) ([f992867](https://github.com/denyzzko/nixpkgs-notifier/commit/f992867e549243d5890f61b2b0e53227fe9b75a8))
* **nixos:** add versioned PostgreSQL data directory option ([6971034](https://github.com/denyzzko/nixpkgs-notifier/commit/697103494c4e0d7d99fd8da8d45c9c3dac73737f))
* notification handling (UI+backend) ([5741fbf](https://github.com/denyzzko/nixpkgs-notifier/commit/5741fbf8f12a47167825426ee064963cc9416a33))
* OIDC - user authentication ([78109d6](https://github.com/denyzzko/nixpkgs-notifier/commit/78109d697b993b5791ca580b9d7dbdfd3c133d1d))
* **oidc:** support dynamic callback URL via X-Forwarded-* headers ([7075bb6](https://github.com/denyzzko/nixpkgs-notifier/commit/7075bb636ca3fc4071dae16cbb5d31a93f6a536c))
* profile menu, admin profile management and some UI improvements ([f290eeb](https://github.com/denyzzko/nixpkgs-notifier/commit/f290eeb4d3a77ddb178b34281a7e9f8456842323))
* querying to db and refactoring ([45585f6](https://github.com/denyzzko/nixpkgs-notifier/commit/45585f6aa12af54165b83fd1ccdaa4984b4310f2))
* retrieving nix package version ([28f6a5a](https://github.com/denyzzko/nixpkgs-notifier/commit/28f6a5ab3466ccac06e8a6aaefd4afb86db3cb3a))
* rework dev-container app with persistent state and OIDC override ([2b7588d](https://github.com/denyzzko/nixpkgs-notifier/commit/2b7588da91884608184d8de8a5dd58e4bf12a223))
* sessions ([6c97e84](https://github.com/denyzzko/nixpkgs-notifier/commit/6c97e845730eaeda15f3800302f416cdd57b3f7b))
* support for mattermost webhook ([5f12c13](https://github.com/denyzzko/nixpkgs-notifier/commit/5f12c133a144ac3d6e91c2a67cd781949be96cb6))
* support for N OIDC Identity Providers (and some comments refactoring) ([7bd7de7](https://github.com/denyzzko/nixpkgs-notifier/commit/7bd7de784a4ea1ddce4596c16646a961846d3f17))
* UI - first pages (stack:htmx+templ+bootstrap) and some refactoring ([0009ba5](https://github.com/denyzzko/nixpkgs-notifier/commit/0009ba5dcc6352a690101a8df9fbe983b6ca6379))
* UI improvements for modern look ([b3749bf](https://github.com/denyzzko/nixpkgs-notifier/commit/b3749bf9d0275a53365b386115ced088a212d1dd))
