package example;

import org.jspecify.annotations.NullMarked;
import org.jspecify.annotations.Nullable;

/**
 * Demonstrates cappu's jspecify nullness checking (nikeee/cappu#25).
 *
 * The class is {@code @NullMarked}, so every unannotated reference type is non-null
 * and {@code @Nullable} marks the exceptions. With
 * {@code "compilerOptions": { "nullness": { "enabled": true } }} in cappu.json the
 * language server reports a warning when a possibly-null value reaches a non-null
 * position - and stays quiet once the code has proven the value non-null (flow-aware
 * narrowing). See the marked lines in {@link #main}.
 */
@NullMarked
public class Main {
    /** A lookup that may miss: its result is explicitly {@code @Nullable}. */
    static @Nullable String lookup(String key) {
        return "greeting".equals(key) ? "hello" : null;
    }

    /** A non-null sink: {@code name} is non-null by the {@code @NullMarked} default. */
    static String shout(String name) {
        return name.toUpperCase() + "!";
    }

    public static void main(String[] args) {
        // NULLNESS WARNING here: lookup(...) is @Nullable but shout() requires a
        // non-null argument. (Runtime-safe in this call, but unchecked in general.)
        System.out.println(shout(lookup("greeting")));

        // No warning: a guard narrows the value to non-null for the rest of the block.
        @Nullable String found = lookup("missing");
        if (found != null) {
            System.out.println(shout(found));
        } else {
            System.out.println(shout("world"));
        }
    }
}
