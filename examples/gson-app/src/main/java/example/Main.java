package example;

import com.google.gson.Gson;

/** One-file example: serialize an object with Gson. */
public class Main {
    record Point(int x, int y) {}

    public static void main(String[] args) {
        System.out.println(new Gson().toJson(new Point(1, 2)));
    }
}
