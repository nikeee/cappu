package example;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.Test;

class MainTest {
    @Test
    void readsMainResource() throws Exception {
        // src/main/resources is on the test classpath via the compiled main tree
        assertEquals("hello from main resources", Main.read("/message.txt"));
    }

    @Test
    void readsTestResource() throws Exception {
        // src/test/resources is on the test runtime classpath
        assertEquals("hello from test resources", Main.read("/test-fixture.txt"));
    }
}
