# Commit navigation

Only commits needed to explain the released state are indexed. All SHA prefixes resolve in this repository.

- `8c7b230d` -> Core/lifecycle -> FEATURE -> OWN
- `4494c962`, `5f56f02c`, `09964183`, `93385793` -> Core/memory -> MEMORY, FIX -> OWN
- `93823c3d`, `8735b1d` -> Core/reuse -> FIX, HARDENING -> OWN
- `e4327dca`, `4c7d96c0`, `ae197b92`, `645d96b4`, `60681f27`, `0ff90482` -> Apple/lifecycle -> FIX, HARDENING -> OWN
- `01262510`, `e3399187`, `31f250ff` -> Physical-path validation -> FEATURE, FIX, HARDENING -> OWN
- `4a7e43d1`, `ef105942`, `fc7b0f17` -> Consumer/DNS demand -> FEATURE -> OWN
- `5b0a33c`, `a123864b`, `ffec0df1`, `8fad3e9b` -> DNS -> FEATURE, FIX -> OWN
- `4ca74d69`, `0dfedb38`, `d017b5bb` -> Routing -> FIX -> OWN
- `e053cc73`, `4dff25c`, `9167595`, `bfe72145`, `578663e0` -> Sniff -> PERF, HARDENING -> OWN
- `b83089ec` -> MASQUE -> FEATURE -> UPSTREAM-INHERITED
- `a367bbe3`, `a00a2fe6`, `0d28c26d`, `9235f90`, `c3b4f567`, `2118ee40` -> MASQUE -> FIX, HARDENING -> OWN
- `f4c0c412` -> Naive dependency -> COMPAT -> UPSTREAM-INHERITED
- `d86e7d73`, `6afd04e6`, `8a30bccf`, `c7014ddc`, `344b22ec` -> Apple build -> BUILD, FIX, TEST -> OWN
- `fb51b507`, `42926537`, `8cbc00fc`, `2a636fae` -> Android ABI/build -> FIX, CI -> OWN
- `982d7ca5`, `a267e99e` -> Windows/release -> FIX, RELEASE -> OWN
- `e63ce659`, `9aef595e`, `d660c979` -> Release gates -> CI, HARDENING -> OWN

Earlier experimental and closure commits are **SUPERSEDED/noise** unless they are cited by a module as a still-relevant trap. Do not use their titles as claims about the release.
