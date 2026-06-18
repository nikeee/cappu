package example;

import org.mapstruct.factory.Mappers;

/** MapStruct generates CarMapperImpl at compile time (cappu runs the
 * annotation processor via javac); this just uses it. */
public class Main {
    public static void main(String[] args) {
        CarDto dto = Mappers.getMapper(CarMapper.class).toDto(new Car("Wartburg 353", 50));
        System.out.println(dto.name() + " / " + dto.horsePower() + " hp");
    }
}
