workspace(name = "gitslice")

load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

http_archive(
    name = "io_bazel_rules_go",
    sha256 = "68af54cb97fbdee5e5e8fe8d210d15a518f9d62abfd71620c3eaff3b26a5ff86",
    urls = [
        "https://github.com/bazel-contrib/rules_go/releases/download/v0.59.0/rules_go-v0.59.0.zip",
    ],
)

http_archive(
    name = "bazel_gazelle",
    sha256 = "2c7545cc914a411e5c15c3d55a0a5ed34fbff62def0e1f0ab6ed14bda222c3cc",
    urls = [
        "https://github.com/bazelbuild/bazel-gazelle/releases/download/v0.39.0/bazel-gazelle-v0.39.0.tar.gz",
    ],
)

http_archive(
    name = "rules_proto",
    sha256 = "303e86e722a520f6f326a50b41cfc16b98fe6d1955ce46642a5b7a67c11c0f5d",
    strip_prefix = "rules_proto-6.0.0",
    urls = [
        "https://github.com/bazelbuild/rules_proto/releases/download/6.0.0/rules_proto-6.0.0.tar.gz",
    ],
)

load("@io_bazel_rules_go//go:deps.bzl", "go_register_toolchains", "go_rules_dependencies")

go_rules_dependencies()

go_register_toolchains(version = "1.24.0")

load("@bazel_gazelle//:deps.bzl", "gazelle_dependencies")

gazelle_dependencies()

load("@rules_proto//proto:repositories.bzl", "rules_proto_dependencies")

rules_proto_dependencies()

load("//:deps.bzl", "go_deps")

go_deps()
