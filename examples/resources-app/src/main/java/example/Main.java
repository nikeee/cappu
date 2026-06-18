package example;

import java.io.InputStream;

/** Reads a classpath resource bundled from src/main/resources. */
public class Main {
    static String read(String resource) throws Exception {
        try (InputStream in = Main.class.getResourceAsStream(resource)) {
            if (in == null) throw new IllegalStateException("missing resource: " + resource);
            return new String(in.readAllBytes()).trim();
        }
    }

    public static void main(String[] args) throws Exception {
        System.out.println(read("/message.txt"));
    }
}
