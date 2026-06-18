package com.example;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

/**
 * A minimal Spring Boot application. {@code SpringApplication.run} boots the
 * application context (auto-configuration, logging, the startup banner) and,
 * with no web server or runners, returns - so the JVM exits once main does.
 */
@SpringBootApplication
public class App {
  public static void main(String[] args) {
    SpringApplication.run(App.class, args);
  }
}
