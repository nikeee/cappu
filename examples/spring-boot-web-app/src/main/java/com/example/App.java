package com.example;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

/**
 * A minimal Spring Boot web application. It starts embedded Tomcat and serves
 * one endpoint - proving the fat jar's merged auto-configuration descriptors
 * bring up the full web stack, not just a bare context.
 */
@SpringBootApplication
@RestController
public class App {
  @GetMapping("/hello")
  public String hello() {
    return "hello from fat jar";
  }

  public static void main(String[] args) {
    SpringApplication.run(App.class, args);
  }
}
