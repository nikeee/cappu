# spring-boot-web-app

A minimal Spring Boot web app (embedded Tomcat) packed into one runnable fat
jar. Where `spring-boot-app` proves the context boots, this proves the full web
stack comes up from a single archive: Tomcat finds Spring's bootstrap through
the merged `META-INF/services/jakarta.servlet.ServletContainerInitializer`, and
auto-configuration through the merged `spring.factories` /
`AutoConfiguration.imports` descriptors (see `spring-boot-app`'s README for why
those same-path descriptors have to be merged rather than overwritten).

```sh
cappu install   # the spring-boot-starter-web tree into .cappu/lib/classes
cappu compile   # one runnable jar -> dist/spring-boot-web-app-1.0.0.jar  ("output": "fat-jar")
java -jar dist/spring-boot-web-app-1.0.0.jar
curl http://localhost:8080/hello   # -> hello from fat jar
```
