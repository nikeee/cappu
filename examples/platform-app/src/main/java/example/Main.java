package example;

import java.sql.DriverManager;
import java.time.LocalDate;
import java.util.List;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializationFeature;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;
import com.google.common.collect.ImmutableList;
import org.jspecify.annotations.NullMarked;
import org.jspecify.annotations.Nullable;

/**
 * One-file example over the dependency features of cappu.json: two Jackson
 * modules versioned by the jackson-bom platform, Guava without its
 * annotation-only transitives, jspecify on the compile classpath only and the
 * H2 driver on the runtime classpath only (this code talks to java.sql).
 */
@NullMarked
public class Main {
    record Release(String name, LocalDate date) {}

    static @Nullable String first(List<String> names) {
        return names.isEmpty() ? null : names.get(0);
    }

    public static void main(String[] args) throws Exception {
        var mapper = new ObjectMapper()
            .registerModule(new JavaTimeModule())
            .disable(SerializationFeature.WRITE_DATES_AS_TIMESTAMPS);
        System.out.println(mapper.writeValueAsString(new Release("1.0.0", LocalDate.of(2026, 1, 1))));

        System.out.println(first(ImmutableList.of("guava", "jackson")));

        try (var connection = DriverManager.getConnection("jdbc:h2:mem:demo");
             var statement = connection.createStatement();
             var rows = statement.executeQuery("SELECT 1 + 1")) {
            rows.next();
            System.out.println(rows.getInt(1));
        }
    }
}
