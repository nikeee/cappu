# spring-boot-app

A minimal Spring Boot app (latest Spring Boot). cappu resolves the whole starter
dependency tree, compiles it, and packs everything into one runnable fat jar.

A naive fat jar breaks Spring Boot: auto-configuration is discovered from
descriptors (`META-INF/spring.factories`, the `AutoConfiguration.imports`,
`META-INF/services/*`) that many jars carry at the SAME path, so flattening them
into one archive would drop all but one copy. cappu's `fat-jar` merges these
same-path descriptors (unioning service lists and `spring.factories` keys), so a
single jar boots exactly like the classpath of separate jars would:

```sh
cappu install   # the spring-boot-starter tree into .cappu/lib/classes
cappu compile   # one runnable jar -> dist/spring-boot-app-1.0.0.jar  ("output": "fat-jar")
java -jar dist/spring-boot-app-1.0.0.jar
```

A WAR is deliberately not produced: that format targets an external servlet
container, whereas the self-contained executable jar is Spring Boot's own
distribution model.
