new_http_archive(
  name = "groovy-sdk-artifact",
  url = "http://dl.bintray.com/groovy/maven/apache-groovy-binary-2.4.4.zip",
  sha256 = "a7cc1e5315a14ea38db1b2b9ce0792e35174161141a6a3e2ef49b7b2788c258c",
  build_file = "groovy.BUILD",
)
bind(
  name = "groovy-sdk",
  actual = "@groovy-sdk-artifact//:sdk",
)

maven_jar(
  name = "junit-artifact",
  artifact = "junit:junit:4.12",
)
bind(
  name = "junit",
  actual = "@junit-artifact//jar",
)
