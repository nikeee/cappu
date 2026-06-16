package example;

import org.apache.logging.log4j.LogManager;
import org.apache.logging.log4j.Logger;

/** A trivial app pinned to a vulnerable Log4j on purpose - run `cappu audit`
 *  in this directory to see the advisories and the dependency tree. */
public class Main {
    private static final Logger log = LogManager.getLogger(Main.class);

    public static void main(String[] args) {
        log.info("hello from a deliberately vulnerable project");
    }
}
