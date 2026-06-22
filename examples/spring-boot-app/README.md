# spring-boot-app

A minimal Spring Boot app (latest Spring Boot). cappu resolves the whole starter
dependency tree and compiles it; it runs from a classpath of the individual jars
(not a fat jar - Spring relies on each jar's separate `META-INF` for
auto-configuration):

```sh
cappu install                 # the spring-boot-starter tree into .cappu/lib/classes
cappu compile -o classes      # app classes into dist/
java -cp "dist:.cappu/lib/classes/*" com.example.App
```
